#!/usr/bin/env bash
set -eo pipefail
readonly CGROUP_MEM_LIMIT_FILE=/sys/fs/cgroup/memory/memory.limit_in_bytes
readonly MIN_MAXMETASPACESIZE=64m
# filename: run-java.sh
# usage: use in place of $JAVA_HOME/bin/java
#   * insert --testlimit <totalMemory> before -jar or classname to override the cgroup limit,
#       for testing behavior of this script outside of a cgroup.
#   * insert --javacmd <command> before -jar or classname to override JRE_HOME- or JAVA_HOME-relative
#       bin/java command
#   * insert --showjava before -jar or classname to print the underlying java command and quit
#   * insert --showmem before -jar or classname to print jvm settings/flags with -version
# explanation: since AWS ECS restricts task placement based on the memory limit
# settings, this script is better than the following java8 params for server applications
# running in ECS tasks:
#   -XX:+UnlockExperimentalVMOptions
#   -XX:+UseCGroupMemoryLimitForHeap
#   -XX:MaxRAMFraction=1
# because the above parameters do not account for metaspace.
usage() {
    echo "$(basename "$0") [ --testlimit <totalMemory> ] [ --javacmd <javaexec> ] [ --showmem or --showjava ] <javaArgs> ..." >&2
    echo "$(basename "$0") [ --help ]" >&2
    echo "" >&2
    echo "  --testlimit <totalMemory>       : Override cgroup memory limit to test this script's behavior outside of a cgroup." >&2
    echo "  --javacmd <javaexec>            : Override JRE_HOME- or JAVA_HOME-relative bin/java command." >&2
    echo "  --showjava                      : Print the underlying java command and quit." >&2
    echo "  --showmem                       : Print jvm settings/flags with -version." >&2
    echo "  --help                          : Print this help message and exit." >&2
    echo "" >&2
    echo "  <javaArgs> ...                  : Specify additional arguments for passing to java executable. See below for special cases:" >&2
    echo "    -XX:MaxMetaspaceSize=?        : If not specified and cgroup limit is in effect, will be set to at least ${MIN_MAXMETASPACESIZE}," >&2
    echo "                                    and at least -XX:MetaspaceSize, if that flag is specified." >&2
    echo "    -Xmx|-XX:MaxHeapSize=?        : May be overridden to fit within cgroup memory limit minus -XX:MaxMetaspaceSize." >&2
    echo "    -Xms|-XX:InitialHeapSize=?    : If specified, may be overridden to fit -Xmx." >&2
}

showMem=""
showJava=""

javaCmd="java"
if [[ "${JRE_HOME}" != "" ]] && [[ -x "${JRE_HOME}/bin/java" ]]; then
    javaCmd="${JRE_HOME}/bin/java"
elif [[ "${JAVA_HOME}" != "" ]] && [[ -x "${JAVA_HOME}/bin/java" ]]; then
    javaCmd="${JAVA_HOME}/bin/java"
elif [[ -f "${HOME}/.javacmd" ]]; then
    javaCmd=$(head -n 1 "${HOME}/.javacmd")
fi


# Sets the initial size (in bytes) of the heap. This value must be a multiple of 1024
# and greater than 1 MB. Append the letter k or K to indicate kilobytes, m or M to
# indicate megabytes, g or G to indicate gigabytes.
# If you don’t set this option, then the initial size is set as the sum of the sizes
# allocated for the old generation and the young generation.
PREFERRED_XMS="0"
# Specifies the maximum size (in bytes) of the memory allocation pool in bytes. This
# value must be a multiple of 1024 and greater than 2 MB. Append the letter k or K to
# indicate kilobytes, m or M to indicate megabytes, or g or G to indicate gigabytes.
# The default value is chosen at runtime based on system configuration. For server
# deployments, -Xms and -Xmx are often set to the same value.
PREFERRED_XMX="0"
# Sets the maximum amount of native memory that can be allocated for class metadata.
# By default, the size isn’t limited. The amount of metadata for an application
# depends on the application itself, other running applications, and the amount of
# memory available on the system.
PREFERRED_XXMAXMETASPACESIZE="0"
# Sets the size of the allocated class metadata space that will trigger a garbage
# collection the first time it is exceeded. This threshold for a garbage collection
# is increased or decreased depending on the amount of metadata used.
PREFERRED_XXMETASPACESIZE="0"

# read the cgroup memory limit file
totalLimit="0"
if [[ -f "$CGROUP_MEM_LIMIT_FILE" ]]; then
    totalLimit=$(head -n 1 "$CGROUP_MEM_LIMIT_FILE")
fi
# java args which are safe to passthru in any context
passthruArgs=()
# java memory flags which may be overridden by cgroup memory limit
memPrefArgs=()
# command args to append to the run a program
programArgs=()

# down convert from G/M/K units to scalar bytes for arithmetic.
javaMemValueToLong() {
    local argValue="$1"
    local base
    local unit

    base=$(echo "$argValue" | sed 's/[^0-9]//g')
    unit=$(echo "$argValue" | sed -n '/[kKmMgG]$/ s/.*\([kKmMgG]\)$/\1/p')

    case "$unit" in
        k|K)    echo $((base * 1024));;
        m|M)    echo $((base * 1024 * 1024));;
        g|G)    echo $((base * 1024 * 1024 * 1024));;
        *)      echo "$base";;
    esac
}

# up convert from bytes to K stripping remainder, or from K to M or G units if possible without stripping remainder.
# otherwise, return original value.
longMemToShorter() {
    local argValue="$1"
    local base
    local unit

    base=$(echo "$argValue" | sed 's/[^0-9]//g')
    unit=$(echo "$argValue" | sed -n '/[kKmMgG]$/ s/.*\([kKmMgG]\)$/\1/p')

    case "$unit" in
        g|G)    echo "${base}${unit}";;
        m|M)    if [[ "$((base % 1024))" -eq "0" ]]; then
                    longMemToShorter "$((base / 1024))G"
                else
                    echo "${base}${unit}"
                fi;;
        k|K)    if [[ "$((base % 1024))" -eq "0" ]]; then
                    longMemToShorter "$((base / 1024))M"
                else
                    echo "${base}${unit}"
                fi;;
        *)      longMemToShorter "$((base / 1024))K"
    esac

}

while [[ $# -gt 0 ]]; do
    opt="$1"
    shift
    case "$opt" in
        # match our custom modes switches using double hyphens, so as not to confuse as java flags
        --help)                 usage
                                exit 1;;
        --testlimit)            totalLimit="$(javaMemValueToLong "$1")"
                                shift
                                ;;
        --showmem)              showMem=true
                                ;;
        --showjava)             showJava=true
                                ;;
        --javacmd)              javaCmd="$1"
                                shift
                                ;;
        -XX:MaxMetaspaceSize=*) PREFERRED_XXMAXMETASPACESIZE="${opt:21}"
                                memPrefArgs=("${memPrefArgs[@]}" "$opt")
                                ;;
        -XX:MetaspaceSize=*)    PREFERRED_XXMETASPACESIZE="${opt:18}"
                                memPrefArgs=("${memPrefArgs[@]}" "$opt")
                                ;;
        -XX:InitialHeapSize=*)  PREFERRED_XMS="${opt:20}"
                                memPrefArgs=("${memPrefArgs[@]}" "$opt")
                                ;;
        -Xms*)                  PREFERRED_XMS="${opt:4}"
                                memPrefArgs=("${memPrefArgs[@]}" "$opt")
                                ;;
        -XX:MaxHeapSize=*)      PREFERRED_XMX="${opt:16}"
                                memPrefArgs=("${memPrefArgs[@]}" "$opt")
                                ;;
        -Xmx*)                  PREFERRED_XMX="${opt:4}"
                                memPrefArgs=("${memPrefArgs[@]}" "$opt")
                                ;;
        -jar)                   # treat -jar as final jvm arg, just like we do `*`
                                # when it doesn't match `-*`
                                programArgs=("$opt" "$@")
                                break
                                ;;
        -cp|-classpath)         # classpath is the only java option other -jar that takes
                                # a value in the next argv element. We need to add both
                                # the opt and the value to passthruArgs and shift.
                                passthruArgs=("${passthruArgs[@]}" "$opt" "$1")
                                shift
                                ;;
        @*)                     # "at-file" params pass file paths containing
                                # jvm flags. hopefully they don't contain one of
                                # the above flags, cause we're not parsing them ATM.
                                passthruArgs=("${passthruArgs[@]}" "$opt")
                                ;;
        -*)                     # passthru any other flags.
                                passthruArgs=("${passthruArgs[@]}" "$opt")
                                ;;
        *)                      programArgs=("$opt" "$@")
                                break
                                ;;
    esac
done

# memory_limit = max heap + max metaspace
jvmSettingOverrides=()

# coerce totalLimit to number.
case "$totalLimit" in
    ''|*[!0-9]*)    totalLimit="0";;    # not a number, default to 0.
    *)              ;;                  # valid number
esac

# the specified max heap value MUST be at least 2m according to oracle.
# compute minimum max heap here
minMaxHeap=$(javaMemValueToLong "2m")
minMaxMetaspace=$(javaMemValueToLong "$MIN_MAXMETASPACESIZE")

# only enforce the memory limit if it is greater than minMaxHeap + minMaxMetaspace. we can't specify max heap
# and metaspace ergonomically with any less. Otherwise, bail to java using command-line args as-is.
if [[ "$totalLimit" -ge "$((minMaxHeap + minMaxMetaspace))" ]]; then
    prefXmx=$(javaMemValueToLong "$PREFERRED_XMX")
    prefMetaspace=$(javaMemValueToLong "$PREFERRED_XXMETASPACESIZE")
    # if -XX:MetaspaceSize is specified and greater than minMaxMetaspace, override the min.
    if [[ "$PREFERRED_XXMETASPACESIZE" != "0" ]]; then
        if [[ "$prefMetaspace" -gt "$minMaxMetaspace" ]]; then
            minMaxMetaspace="$prefMetaspace"
        fi
    fi

    # We will always need to set -XX:MaxMetaspaceSize in a cgroup context, since it is otherwise unlimited.
    # When not specified explicitly, make it as unlimited as possible if Xmx was set explicitly. Otherwise,
    # use the minimum as a default, and proceed to restricting Xmx ergonomically.
    if [[ "$PREFERRED_XXMAXMETASPACESIZE" == "0" ]]; then
        # if -Xmx is explicitly set, and leaves more than the minimum MaxMetaspaceSize when subtracted from
        # the cgroup limit, use whatever is left
        if [[ "$PREFERRED_XMX" != "0" ]] && [[ "$prefXmx" -lt "$((totalLimit - minMaxMetaspace))" ]]; then
            # preferred -Xmx is within limit with minMaxMetaspaceSize.
            # use preferred -Xmx and allocate the rest for metaspace
            ergoMss=$((totalLimit - prefXmx))
            # convert to at least K units to ensure multiple of 1024
            mssk="$(longMemToShorter $ergoMss)"
            PREFERRED_XXMAXMETASPACESIZE="$mssk"
        else
            # use minMaxMetaspace as sane default max value when no preference is set on the command line.
            PREFERRED_XXMAXMETASPACESIZE="$(longMemToShorter "$minMaxMetaspace")"
        fi
    fi

    # insert metaspace first, so that it is most visible in `ps -ef` for troubleshooting.
    jvmSettingOverrides=("${jvmSettingOverrides[@]}" "-XX:MaxMetaspaceSize=${PREFERRED_XXMAXMETASPACESIZE}")
    # append -XX:MetaspaceSize if it was specified.
    if [[ "$PREFERRED_XXMETASPACESIZE" != "0" ]]; then
        jvmSettingOverrides=("${jvmSettingOverrides[@]}" "-XX:MetaspaceSize=${PREFERRED_XXMETASPACESIZE}")
    fi

    maxMetaspace=$(javaMemValueToLong "$PREFERRED_XXMAXMETASPACESIZE")
    ergoXmx=$((totalLimit - maxMetaspace))

    # -Xmx should be set ergonomically if:
    # 1. it was not set explicitly, or
    # 2. the ergo value is less than the explicitly preferred value
    if [[ "$PREFERRED_XMX" == "0" ]] || [[ "$ergoXmx" -lt "$prefXmx" ]]; then
        # convert to at least K units to ensure multiple of 1024
        xmxk="$(longMemToShorter $ergoXmx)"
        jvmSettingOverrides=("${jvmSettingOverrides[@]}" -Xmx"$xmxk")
        # only specify -Xms if it was explicitly set originally.
        if [[ "$PREFERRED_XMS" != "0" ]]; then
            prefXms=$(javaMemValueToLong "$PREFERRED_XMS")
            if [[ "$prefXms" -lt "$ergoXmx" ]]; then
                jvmSettingOverrides=("${jvmSettingOverrides[@]}" -Xms"$PREFERRED_XMS")
            else
                jvmSettingOverrides=("${jvmSettingOverrides[@]}" -Xms"$xmxk")
            fi
        fi
    else
        # preferred values are safe with max metaspace and cgroup memory limit. add them back as-is.
        if [[ "$PREFERRED_XMX" != "0" ]]; then
            jvmSettingOverrides=("${jvmSettingOverrides[@]}" "-Xmx${PREFERRED_XMX}")
        fi

        if [[ "$PREFERRED_XMS" != "0" ]]; then
            jvmSettingOverrides=("${jvmSettingOverrides[@]}" "-Xms${PREFERRED_XMS}")
        fi
    fi
else
    # if not in cgroup jail, use any preferred memory settings as-is
    jvmSettingOverrides=("${memPrefArgs[@]}")
fi

fullJvm=("$javaCmd" "${jvmSettingOverrides[@]}" "${passthruArgs[@]}")

if [[ "$showMem" == "true" ]]; then
    ("${fullJvm[@]}" -XshowSettings:vm -XX:+PrintCommandLineFlags -version)
elif [[ "$showJava" == "true" ]]; then
    echo "${fullJvm[@]}" "${programArgs[@]}"
else
    "${fullJvm[@]}" "${programArgs[@]}"
fi
