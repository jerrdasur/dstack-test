**Functionality**
* The program should create a Docker container using the given Docker image name,
and the given bash command
* The program should handle the output logs of the container and send them to the
given AWS CloudWatch group/stream using the given AWS credentials. If the
corresponding AWS CloudWatch group or stream does not exist, it should create it
using the given AWS credentials.
* 
**Other requirements**
* The program should behave properly regardless of how much or what kind of logs
the container outputs
* The program should gracefully handle errors and interruption