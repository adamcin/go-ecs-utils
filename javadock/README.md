javadock
========

Wrapper for Java 8/9 `$JAVA_HOME/bin/java` for use in docker containers, where the JVM settings must adjusted to conform to cgroup memory limits.

Essentially, the issue with `-XX:+UseCGroupMemoryLimitForHeap` in Java 8u131+ is that it 1) doesn't account for Metaspace, and 2) in combination with
-XX:MaxRAMFraction, it is impossible to allocate a fraction between 0% and 50% of available memory to Metaspace.

This command rewrites the JVM memory setting arguments before being passed through to `$JAVA_HOME/bin/java` to make sure that a specific, or at least
minimum `-XX:MaxMetaspaceSize` is set, and that the `-Xmx` and `-Xms` are reduced, if necessary, such that `MaxMetaspaceSize >= 64m`,
`MetaspaceSize <= MaxMetaspaceSize`, `InitialHeapSize <= MaxHeapSize`, and `MaxHeapSize + MaxMetaspaceSize <= CGroupMemLimit`.
