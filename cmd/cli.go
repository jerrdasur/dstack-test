package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	typesDocker "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/urfave/cli/v2"
	"io"
	"log"
	"os"
	"time"
)

const (
	argDockerImage        = "docker-image"
	argBashCommand        = "bash-command"
	argCloudwatchGroup    = "cloudwatch-group"
	argCloudwatchStream   = "cloudwatch-stream"
	argAwsAccessKeyID     = "aws-access-key-id"
	argAwsSecretAccessKey = "aws-secret-access-key"
	argAwsRegion          = "aws-region"
)

var (
	errResourceAlreadyExists = &types.ResourceAlreadyExistsException{}

	appFlags = []cli.Flag{
		&cli.StringFlag{
			Name:     argDockerImage,
			Usage:    "name of docker image with pre-installed BASH for usage",
			Required: true,
		},
		&cli.StringFlag{
			Name:     argBashCommand,
			Usage:    "command to run inside of the container with BASH interpreter",
			Required: true,
		},
		&cli.StringFlag{
			Name:     argCloudwatchGroup,
			Usage:    "name of group for logs",
			Required: true,
		},
		&cli.StringFlag{
			Name:     argCloudwatchStream,
			Usage:    "name of stream for logs",
			Required: true,
		},
		&cli.StringFlag{
			Name:     argAwsAccessKeyID,
			Usage:    "credential for creating missed group/stream in CloudWatch",
			Required: true,
		},
		&cli.StringFlag{
			Name:     argAwsSecretAccessKey,
			Usage:    "credential for creating missed group/stream in CloudWatch",
			Required: true,
		},
		&cli.StringFlag{
			Name:     argAwsRegion,
			Usage:    "credential for creating missed group/stream in CloudWatch",
			Required: true,
		},
	}
)

type awsCreds struct {
	keyID     string
	secretKey string
}

func (ac awsCreds) Retrieve(ctx context.Context) (aws.Credentials, error) {
	return aws.Credentials{
		AccessKeyID:     ac.keyID,
		SecretAccessKey: ac.secretKey,
	}, nil
}

func main() {
	app := &cli.App{
		Flags: appFlags,
		Action: func(ctx *cli.Context) error {
			logsClient := cloudwatchlogs.NewFromConfig(aws.Config{
				Region: ctx.String(argAwsRegion),
				Credentials: awsCreds{
					keyID:     ctx.String(argAwsAccessKeyID),
					secretKey: ctx.String(argAwsSecretAccessKey),
				},
				RetryMaxAttempts: 10,
				RetryMode:        aws.RetryModeStandard,
				ClientLogMode:    aws.LogRequestEventMessage | aws.LogResponseEventMessage,
			})

			_, err := logsClient.CreateLogGroup(ctx.Context, &cloudwatchlogs.CreateLogGroupInput{
				LogGroupName: nullString(ctx.String(argCloudwatchGroup)),
			})
			if err != nil && !errors.As(err, &errResourceAlreadyExists) {
				return fmt.Errorf("CreateLogGroup: %w", err)
			}

			_, err = logsClient.CreateLogStream(ctx.Context, &cloudwatchlogs.CreateLogStreamInput{
				LogGroupName:  nullString(ctx.String(argCloudwatchGroup)),
				LogStreamName: nullString(ctx.String(argCloudwatchStream)),
			})
			if err != nil && !errors.As(err, &errResourceAlreadyExists) {
				return fmt.Errorf("CreateLogStream: %w", err)
			}

			dockerClient, err := client.NewClientWithOpts(client.FromEnv)
			if err != nil {
				return fmt.Errorf("failed to connect to docker: %w", err)
			}

			respBody, err := dockerClient.ImagePull(ctx.Context, ctx.String(argDockerImage), typesDocker.ImagePullOptions{})
			if err != nil {
				return fmt.Errorf("failed to pull the image: %w", err)
			}
			for {
				data := make([]byte, 8)
				_, err = respBody.Read(data)
				if err == io.EOF {
					_ = respBody.Close()
					break
				}
				if err != nil {
					return fmt.Errorf("failed to finish process of pulling")
				}
			}

			containerCreateInfo, err := dockerClient.ContainerCreate(ctx.Context, &container.Config{
				Image:       ctx.String(argDockerImage),
				Shell:       []string{ctx.String(argBashCommand)},
				ArgsEscaped: true,
			}, &container.HostConfig{}, nil, nil, "")
			if err != nil {
				return fmt.Errorf("failed to create the container: %w", err)
			}
			containerID := containerCreateInfo.ID

			err = dockerClient.ContainerStart(ctx.Context, containerID, typesDocker.ContainerStartOptions{})
			if err != nil {
				return fmt.Errorf("failed to start the container: %w", err)
			}

			logsReader, err := dockerClient.ContainerLogs(context.Background(), containerID, typesDocker.ContainerLogsOptions{
				ShowStderr: true,
				ShowStdout: true,
				Follow:     true,
			})
			if err != nil {
				return fmt.Errorf("failed to connect to docker logs: %w", err)
			}

			hdr := make([]byte, 8)
			var logSequenceToken *string
			for {
				_, err := logsReader.Read(hdr)
				if err == io.EOF {
					break
				} else if err != nil {
					return fmt.Errorf("failed to read logs data")
				}

				count := binary.BigEndian.Uint32(hdr[4:])
				dat := make([]byte, count)
				_, err = logsReader.Read(dat)

				fmt.Println(string(dat))
				resp, err := logsClient.PutLogEvents(ctx.Context, &cloudwatchlogs.PutLogEventsInput{
					LogEvents: []types.InputLogEvent{
						{
							Message:   nullString(string(dat)),
							Timestamp: nullInt64(time.Now().Unix()),
						},
					},
					LogGroupName:  nullString(ctx.String(argCloudwatchGroup)),
					LogStreamName: nullString(ctx.String(argCloudwatchStream)),
					SequenceToken: logSequenceToken,
				})
				if err != nil {
					fmt.Println(fmt.Errorf("failed to send logs: %w", err))

					err = dockerClient.ContainerStop(ctx.Context, containerID, nil)
					if err != nil {
						return fmt.Errorf("failed to stop container properly: %w", err)
					}
				}

				logSequenceToken = resp.NextSequenceToken
			}

			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

func nullString(x string) *string {
	return &x
}

func nullInt64(x int64) *int64 {
	return &x
}
