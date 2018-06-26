/*
 * Copyright 2018 Mark Adamcin
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

const CGroupMemLimitFile = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
const MinimumMaxMetaSpaceSize = "64m"
const XXMaxMetaSpaceSize = "-XX:MaxMetaspaceSize="
const XXMetaSpaceSize = "-XX:MetaspaceSize="
const XXInitialHeapSize = "-XX:InitialHeapSize="
const XXMaxHeapSize = "-XX:MaxHeapSize="
const Xms = "-Xms"
const Xmx = "-Xmx"

func usage() {
	argHelp := `%s [ --testlimit <totalMemory> ] [ --javacmd <javaexec> ] [ --showmem or --showjava ] <javaArgs> ...
  --testlimit <totalMemory>       : Override cgroup memory limit to test this script's behavior outside of a cgroup.
  --javacmd <javaexec>            : Specify the java command to use. Will search the PATH for relative paths. Overrides JRE_HOME- or 
                                    JAVA_HOME-relative bin/java command.
  --showjava                      : Print the underlying java command and quit.
  --showmem                       : Print jvm settings/flags with -version.
  --help                          : Print this help message and exit.

  <javaArgs> ...                  : Specify additional arguments for passing to java executable. See below for special cases:
    -XX:MaxMetaspaceSize=?        : If not specified and cgroup limit is in effect, will be set to at least %s, 
                                    and at least -XX:MetaspaceSize, if that flag is specified.
    -Xmx|-XX:MaxHeapSize=?        : May be overridden to fit within cgroup memory limit minus -XX:MaxMetaspaceSize.
    -Xms|-XX:InitialHeapSize=?    : If specified, may be overridden to fit -Xmx.
`
	fmt.Printf(argHelp, filepath.Base(os.Args[0]), MinimumMaxMetaSpaceSize)
}

type ParsedArgs struct {
	// If greater than 0 override system/cgroup total memory limit.
	TestLimit int64

	// If non-empty, override JRE_HOME/bin/java and JAVA_HOME/bin/java. will search PATH if necessary.
	JavaCmd string

	// Sets the maximum amount of native memory that can be allocated for class metadata.
	// By default, the size isn’t limited. The amount of metadata for an application
	// depends on the application itself, other running applications, and the amount of
	// memory available on the system.
	PrefMaxMeta int64

	// Sets the size of the allocated class metadata space that will trigger a garbage
	// collection the first time it is exceeded. This threshold for a garbage collection
	// is increased or decreased depending on the amount of metadata used.
	PrefMeta int64

	// Sets the initial size (in bytes) of the heap. This value must be a multiple of 1024
	// and greater than 1 MB. Append the letter k or K to indicate kilobytes, m or M to
	// indicate megabytes, g or G to indicate gigabytes.
	// If you don’t set this option, then the initial size is set as the sum of the sizes
	// allocated for the old generation and the young generation.
	PrefInitHeap int64

	// Specifies the maximum size (in bytes) of the memory allocation pool in bytes. This
	// value must be a multiple of 1024 and greater than 2 MB. Append the letter k or K to
	// indicate kilobytes, m or M to indicate megabytes, or g or G to indicate gigabytes.
	// The default value is chosen at runtime based on system configuration. For server
	// deployments, -Xms and -Xmx are often set to the same value.
	PrefMaxHeap int64

	// Modal switches
	ShowHelp, ShowMem, ShowJava bool

	// collect remaining arguments for downstream calls
	PassthruArgs, MemPrefArgs, ProgramArgs []string
}

func parseArgs() ParsedArgs {
	showHelp := false
	showMem := false
	showJava := false

	testLimit := int64(0)
	javacmd := ""

	passthruArgs := make([]string, 0)
	memPrefArgs := make([]string, 0)
	programArgs := make([]string, 0)

	prefMaxMeta := "0"
	prefMeta := "0"
	prefInitHeap := "0"
	prefMaxHeap := "0"

	for i := 1; i < len(os.Args); i++ {
		opt := os.Args[i]
		if opt == "--help" {
			showHelp = true
		} else if opt == "--showmem" {
			showMem = true
		} else if opt == "--showjava" {
			showJava = true
		} else if opt == "--testlimit" {
			if len(os.Args) > i+1 {
				testLimit = parseMem(os.Args[i+1])
				i = i + 1
			}
		} else if opt == "--javacmd" {
			if len(os.Args) > i+1 {
				javacmd = os.Args[i+1]
				i = i + 1
			}
		} else if strings.HasPrefix(opt, XXMaxMetaSpaceSize) {
			prefMaxMeta = strings.TrimPrefix(opt, XXMaxMetaSpaceSize)
			memPrefArgs = append(memPrefArgs, opt)
		} else if strings.HasPrefix(opt, XXMetaSpaceSize) {
			prefMeta = strings.TrimPrefix(opt, XXMetaSpaceSize)
			memPrefArgs = append(memPrefArgs, opt)
		} else if strings.HasPrefix(opt, XXInitialHeapSize) {
			prefInitHeap = strings.TrimPrefix(opt, XXInitialHeapSize)
			memPrefArgs = append(memPrefArgs, opt)
		} else if strings.HasPrefix(opt, Xms) {
			prefInitHeap = strings.TrimPrefix(opt, Xms)
			memPrefArgs = append(memPrefArgs, opt)
		} else if strings.HasPrefix(opt, XXMaxHeapSize) {
			prefMaxHeap = strings.TrimPrefix(opt, XXMaxHeapSize)
			memPrefArgs = append(memPrefArgs, opt)
		} else if strings.HasPrefix(opt, Xmx) {
			prefMaxHeap = strings.TrimPrefix(opt, Xmx)
			memPrefArgs = append(memPrefArgs, opt)
		} else if opt == "-jar" {
			// treat -jar as final jvm arg, just like we do `*`
			// when it doesn't match `-*`
			programArgs = os.Args[:i]
			break
		} else if opt == "-cp" || opt == "-classpath" {
			// classpath is the only java option other -jar that takes
			// a value in the next argv element. We need to add both
			//the opt and the value to passthruArgs and shift.
			passthruArgs = append(passthruArgs, opt, os.Args[i+1])
			i = i + 1
		} else if strings.HasPrefix(opt, "@") {
			// "at-file" params pass file paths containing
			// jvm flags. hopefully they don't contain one of
			// the above flags, cause we're not parsing them ATM.
			passthruArgs = append(passthruArgs, opt)
		} else if strings.HasPrefix(opt, "-") {
			// passthru any other flags.
			passthruArgs = append(passthruArgs, opt)
		} else {
			// treat everything else as a program argument
			programArgs = os.Args[:i]
			break
		}
	}

	return ParsedArgs{
		ShowHelp:     showHelp,
		ShowJava:     showJava,
		ShowMem:      showMem,
		TestLimit:    testLimit,
		JavaCmd:      javacmd,
		PassthruArgs: passthruArgs,
		MemPrefArgs:  memPrefArgs,
		ProgramArgs:  programArgs,
		PrefMaxMeta:  parseMem(prefMaxMeta),
		PrefMeta:     parseMem(prefMeta),
		PrefInitHeap: parseMem(prefInitHeap),
		PrefMaxHeap:  parseMem(prefMaxHeap)}
}

func unitToPow(unit string) uint {
	switch strings.ToLower(unit) {
	case "k":
		return 1
	case "m":
		return 2
	case "g":
		return 3
	default:
		return 0
	}
}

func parseMem(mem string) int64 {
	baser, _ := regexp.Compile("^[0-9]+")
	base := baser.FindString(mem)
	if base == "" {
		log.Fatal("Failed to parse memory value " + mem)
	}

	baseInt, err := strconv.ParseInt(base, 10, 64)
	if err != nil {
		log.Fatal("Failed to parse memory value " + mem)
	}

	uniter, _ := regexp.Compile("[kKmMgG]?$")
	unit := uniter.FindString(mem)

	return longMem(baseInt, unitToPow(unit))
}

func shortMem(base int64, pow uint) (int64, uint) {
	if pow < 0 {
		log.Fatal("Somehow pow is less than 0")
	} else if pow > 3 {
		return shortMem(base<<10, pow-1)
	} else if pow == 3 {
		return base, pow
	} else if base >= 1024 && (pow == 0 || base%1024 == 0) {
		// always shorten to K regardless of 1024divis., and upgrade to M or G if evenly divisible by 1024.
		return shortMem(base>>10, pow+1)
	}
	return base, pow
}

func longMem(base int64, pow uint) int64 {
	return base << (10 * pow)
}

func fmtMem(base int64, pow uint) string {
	shortBase, shortPow := shortMem(base, pow)
	baseStr := strconv.FormatInt(shortBase, 10)
	switch shortPow {
	case 1:
		return baseStr + "K"
	case 2:
		return baseStr + "M"
	case 3:
		return baseStr + "G"
	default:
		return baseStr
	}
}

// ErrNotFound is the error resulting if a path search failed to find an executable file.
func findExecutable(file string) error {
	d, err := os.Stat(file)
	if err != nil {
		return err
	}
	if m := d.Mode(); !m.IsDir() && m&0111 != 0 {
		return nil
	}
	return os.ErrPermission
}

// modified version of LookPath that skips argv0 if found in the path, which should
// allow linking this as "java" into the path.
// LookPath searches for an executable binary named file
// in the directories named by the PATH environment variable.
// If file contains a slash, it is tried directly and the PATH is not consulted.
// The result may be an absolute path or a path relative to the current directory.
func lookPathNotMe(file string) (string, error) {
	// NOTE(rsc): I wish we could use the Plan 9 behavior here
	// (only bypass the path if file begins with / or ./ or ../)
	// but that would not match all the Unix shells.
	if strings.Contains(file, "/") {
		err := findExecutable(file)
		if err == nil {
			return file, nil
		}
		return "", &exec.Error{file, err}
	}
	path := os.Getenv("PATH")
	me, merr := filepath.Abs(os.Args[0])
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			// Unix shell semantics: path element "" means "."
			dir = "."
		}
		path := filepath.Join(dir, file)
		if merr == nil && path == me {
			// skip me in case I was found on the path
			continue
		}
		if err := findExecutable(path); err == nil {
			return path, nil
		}
	}
	return "", &exec.Error{file, exec.ErrNotFound}
}

func determineJavaExecutable(javacmd string) (string, error) {
	javaExec := "java"
	if len(os.Getenv("JRE_HOME")) > 0 {
		javaExec = os.Getenv("JRE_HOME") + "/bin/java"
	} else if len(os.Getenv("JAVA_HOME")) > 0 {
		javaExec = os.Getenv("JAVA_HOME") + "/bin/java"
	}

	if len(javacmd) > 0 {
		javacmd, err := lookPathNotMe(javacmd)
		if err != nil {
			return "", err
		} else {
			javaExec = javacmd
		}
	}

	return javaExec, nil
}

func determineTotalMemLimit(testlimit int64) int64 {
	totalLimit := int64(0)
	if testlimit > 0 {
		totalLimit = testlimit
	} else {
		content, err := ioutil.ReadFile(CGroupMemLimitFile)
		if err == nil {
			totalLimit = parseMem(fmt.Sprintf("0%s", content))
		}
	}
	return totalLimit
}

func main() {
	prefs := parseArgs()

	if prefs.ShowHelp {
		usage()
		os.Exit(1)
	}

	javaExec, err := determineJavaExecutable(prefs.JavaCmd)
	if err != nil {
		log.Fatal("Failed to determine java executable. ", err)
	}

	totalLimit := determineTotalMemLimit(prefs.TestLimit)

	// memory_limit = max heap + max metaspace
	jvmSet := make([]string, 0)

	// the specified max heap value MUST be at least 2m according to oracle.
	// compute minimum max heap here
	minMaxHeap := parseMem("2m")
	minMaxMetaspace := parseMem(MinimumMaxMetaSpaceSize)

	// only enforce the memory limit if it is greater than minMaxHeap + minMaxMetaspace. we can't specify max heap
	// and metaspace ergonomically with any less. Otherwise, bail to java using command-line args as-is.
	if totalLimit > minMaxHeap+minMaxMetaspace {
		// if -XX:MetaspaceSize is specified and greater than minMaxMetaspace, override the min.
		if prefs.PrefMeta > 0 {
			if prefs.PrefMeta > minMaxMetaspace {
				minMaxMetaspace = prefs.PrefMeta
			}
		}

		// We will always need to set -XX:MaxMetaspaceSize in a cgroup context, since it is otherwise unlimited.
		// When not specified explicitly, make it as unlimited as possible if Xmx was set explicitly. Otherwise,
		// use the minimum as a default, and proceed to restricting Xmx ergonomically.
		if prefs.PrefMaxMeta == 0 {
			// if -Xmx is explicitly set, and leaves more than the minimum MaxMetaspaceSize when subtracted from
			// the cgroup limit, use whatever is left
			if prefs.PrefMaxHeap > 0 && prefs.PrefMaxHeap < totalLimit-minMaxMetaspace {
				// preferred -Xmx is within limit with minMaxMetaspaceSize.
				// use preferred -Xmx and allocate the rest for metaspace
				prefs.PrefMaxMeta = totalLimit - prefs.PrefMaxHeap
			} else {
				// use minMaxMetaspace as sane default max value when no preference is set on the command line.
				prefs.PrefMaxMeta = minMaxMetaspace
			}
		}

		// insert metaspace first, so that it is most visible in `ps -ef` for troubleshooting.
		jvmSet = append(jvmSet, XXMaxMetaSpaceSize+fmtMem(prefs.PrefMaxMeta, 0))

		// append -XX:MetaspaceSize if it was specified.
		if prefs.PrefMeta > 0 {
			jvmSet = append(jvmSet, XXMetaSpaceSize+fmtMem(prefs.PrefMeta, 0))
		}

		maxMetaspace := prefs.PrefMaxMeta
		ergoXmx := totalLimit - maxMetaspace

		// -Xmx should be set ergonomically if:
		// 1. it was not set explicitly, or
		// 2. the ergo value is less than the explicitly preferred value
		if prefs.PrefMaxHeap > 0 || ergoXmx < prefs.PrefMaxHeap {
			// convert to at least K units to ensure multiple of 1024
			jvmSet = append(jvmSet, Xmx+fmtMem(ergoXmx, 0))
			// only specify -Xms if it was explicitly set originally.
			if prefs.PrefInitHeap > 0 {
				if prefs.PrefInitHeap < ergoXmx {
					jvmSet = append(jvmSet, Xms+fmtMem(prefs.PrefInitHeap, 0))
				} else {
					jvmSet = append(jvmSet, Xms+fmtMem(ergoXmx, 0))
				}
			}
		} else {
			// preferred values are safe with max metaspace and cgroup memory limit. add them back as-is.
			if prefs.PrefMaxHeap > 0 {
				jvmSet = append(jvmSet, Xmx+fmtMem(prefs.PrefMaxHeap, 0))
			}

			if prefs.PrefInitHeap > 0 {
				jvmSet = append(jvmSet, Xmx+fmtMem(prefs.PrefInitHeap, 0))
			}
		}
	} else {
		jvmSet = prefs.MemPrefArgs
	}

	jvmArgs := append(jvmSet, prefs.PassthruArgs...)

	if prefs.ShowMem {
		showmemArgs := append(jvmArgs, "-XshowSettings:vm", "-XX:+PrintCommandLineFlags", "-version")
		if err := syscall.Exec(javaExec, showmemArgs, os.Environ()); err != nil {
			log.Fatal(err)
		}
	} else if prefs.ShowJava {
		fmt.Println(javaExec, strings.Join(append(jvmArgs, prefs.ProgramArgs...), " "))
	} else {
		// exec the java executable with the collected arguments
		// we must use javaExec both as argv0 AND as argv[0]
		if err := syscall.Exec(javaExec, append(append([]string{javaExec}, jvmArgs...), prefs.ProgramArgs...), os.Environ()); err != nil {
			log.Fatal(err)
		}
	}
}
