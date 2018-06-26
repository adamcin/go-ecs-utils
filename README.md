go-ecs-utils
============

A collection of utilities originally written in bash and java to support AWS ECS infrastructure and deployment management, now ported to golang to
maximize docker good-citizenship by eliminating external runtime dependencies and linux-specific path dependencies.

My intention is to integrate into multi-stage docker builds, copying the executables into the PATHs of subsequent stages.

At this point, each of these executables are completely self-contained after build. They do not depend on each other, and do not depend on any
external filesystem dependencies apart from the kernel.


overrun
-------

Wrapper for the `aws ecs run-task` command.


jvshim
------

Wrapper for $JAVA_HOME/bin/java, versions 8 and 9, which do not natively support cgroup memory limits.


ssmple
------

Config File Management tool using AWS SSM Shared Parameter Store. Useful for building environment-configured spring apps docker images.
