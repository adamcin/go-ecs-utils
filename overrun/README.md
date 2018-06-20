overrun
=======

Wrapper for the `aws ecs run-task` command that accomplishes the following:

* Minimizes task parameter boilerplate by requiring an existing TaskDefinition and exposing a uniform argv interface for overridable parameters.
  No JSON snippets required.

* Minimizes the challenge of properly escaping ad-hoc shell commands by treating `--` as a delimiter followed by the command and its arguments,
  separated by IFS, leaving it only up to the user to escape tokens that are significant to their current shell, like `$`, `;` and `&&`, when
  appropriate.

* Integrates with the awslogs driver and CloudWatch to stream log event messages to stdout. `overrun` log output is isolated to stderr.

* Exits with the same exit code as the primary task container if the command terminates normally.

* Responds to SIGINT by sending an `aws ecs stop-task` request as soon as possible, in case you realize after submitting the task that you
  made a terrible mistake and start mashing Ctrl-C like a crazy person.

* Exposes a set of flexible arguments for FARGATE execution that accept a combination of resource IDs (`subnet-`, `sg-`, `i-`), Name tags, and EC2
  filters (`tag:Env=prod`, `Name=availabilityZone,Values=us-west-2b,us-west-2a`, etc) for construction-by-query of the `awsvpc` network
  configuration, which otherwise requires specific `subnet-` and `sg-` identifiers when used in the `aws ecs run-task` CLI command.

Internal escaping of arguments is straightforward for construction of the `Command` array. Arguments containing a space are wrapped
with double-quotes after backslash-escaping existing double-quotes within the token. Shell escaping can be disabled with the `--no-shell` switch,
which causes the argv arguments to be passed to the task request as an unjoined, unescaped array.


