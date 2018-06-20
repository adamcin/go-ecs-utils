ssmple CLI
==========

Config File Management tool using AWS SSM Shared Parameter Store, serialized to JSON, YAML, or Java properties files on hosts.

JSON, YAML support are TBD. see [ssmple-java](https://github.com/adamcin/ssmple)

Usage
-----

```
./ssmple --profile myprofile --region us-east-1 -C /ep/conf \
    -f ep.properties \
    -f ep.override.properties \
    -s /ep/ecs/conf \
    -s /ep/ecs/conf/preprod \
    -s /ep/ecs/conf/admin \
    -s /ep/ecs/conf/preprod/admin
```

