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
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/external"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

func usage() {
	argHelp := `%s -c cluster -t taskDef [ <opt> ... ] -- command [ <arg> ... ]
  -h | --help                   : print this help message
  -p | --profile                : set AWS profile
  -r | --region                 : set AWS region
  -t | --task-def               : Base ECS task definition/family/ARN (see aws ecs run-task help for --task-definition)
  -c | --cluster                : ECS Cluster on which to run the task.
  -n | --container-name         : Specify name of container definition to override. By default, will use the first found in base task definition.
  -x | --dry-run                : Construct aws-cli command but print command instead of running it.
  -w | --wait                   : Run task and wait for completion.
  -l | --stream-log             : Run task and begin tailing log stream.
  -e | --env <name[=value]>     : Override environment variables. If =value is not specified, the value for the specified name will be read from this
                                  command's environment.
       --env-file               : Override container environment variables using a specifed env-file. 
       --cpu                    : Override container CPU requirement. 
       --mem                    : Override container Memory limit.
       --mem-res                : Override container Memory Reservation.
       --exec-role              : Override the associated Execution Role ARN.
       --task-role              : Override the associated Task Role ARN.
       --shell <prefix>         : Specify a shell to use to run the command. Must be a prefix for running a single-quoted string argument as a
                                  command, which will be appended with a leading space after construction.
       --no-shell               : Disable quoting as a shell command. Overrides --shell preference.

  -- <command> [ <arg> ... ]    : Override the task container command, 

FARGATE                         : Specifying the following arguments implies using the FARGATE launch type.
  -f      | --fargate           : Activates fargate execution and accepts 0-n resource filters that apply to all taggable EC2 objects.
  -f:ip   | --fargate:ip        : Request Fargate assign a public IP address to the container.
  -f:vpc  | --fargate:vpc       : Filter network config resources by VPCs matching the specified VPC filter. 
  -f:net  | --fargate:net       : Choose network configs based on a subnet ID or a tag=value pair attached to the desired subnet(s).
                                  The default VPC security group is selected by default.
  -f:host | --fargate:host      : Build network configuration to match a running EC2 instance. This will set desired security groups and subnets based on
                                  the particular configuration of the host.
  -f:sg	  | --fargate:sg        : Specify additional security groups by 'sg-' ID or by tag=value, to be attached to the task.
`
	fmt.Printf(argHelp, filepath.Base(os.Args[0]))
}

const (
	FilterModeCluster = iota
	FilterModeHost    = iota
	FilterModeNetwork = iota
)

type ParsedArgs struct {
	AwsProfile, AwsRegion string

	Cluster string

	TaskDef string

	ContainerName string

	Environment map[string]string

	DryRun bool

	WaitStopped, StreamLog bool

	Cpu int64

	Memory int64

	MemoryReservation int64

	ExecRoleArn string

	TaskRoleArn string

	ShellPrefix string

	NoShell bool

	LaunchFargate bool

	FilterMode int

	// filters applied to all fargate net config queries.
	AnyFilters []ec2.Filter

	// filters evaluated to find a single vpc to use as an additional
	// filter for -fg:net, -fg:host, and -fg:sg.
	VpcFilters  []ec2.Filter
	DoFilterVpc bool

	VpcSgFilters []ec2.Filter
	DoFilterSgs  bool

	VpcNetFilters []ec2.Filter

	VpcHostFilters []ec2.Filter

	NetPublicIp bool

	OverridesCmd bool

	CmdOverride []string
}

const NoOptPrefix = "--no-"

func parseArgs() ParsedArgs {
	awsProfile := ""
	awsRegion := ""
	taskDef := ""
	cluster := ""
	containerName := ""
	dryRun := false
	streamLog := false
	waitStopped := false
	execRoleArn := ""
	taskRoleArn := ""
	shellPrefix := ""
	noShell := false

	cpu := int64(0)
	memory := int64(0)
	memoryReservation := int64(0)

	var envOverrides []string

	overridesCmd := false
	var cmdOverride []string

	launchFargate := false
	netPublicIp := false

	filterMode := FilterModeCluster

	var anyFilters []ec2.Filter
	var vpcFilters []ec2.Filter
	doFilterVpc := false
	var vpcSgFilters []ec2.Filter
	doFilterSgs := false
	var vpcNetFilters []ec2.Filter
	var vpcHostFilters []ec2.Filter

	readFilterArgs := func(defaultFilter *string, optToEnd ...string) (int, []ec2.Filter) {
		var filters []ec2.Filter
		for _, optArg := range optToEnd {
			valid, filter := ParseEc2Filter(optArg, defaultFilter)
			if valid {
				filters = append(filters, filter)
			} else {
				break
			}
		}
		return len(filters), filters
	}

ArgLoop:
	for i := 1; i < len(os.Args); i++ {
		opt := os.Args[i]
		isNoOpt := strings.HasPrefix(opt, NoOptPrefix)
		if isNoOpt {
			opt = "--" + strings.TrimPrefix(opt, NoOptPrefix)
		}

		if strings.HasPrefix(opt, "-f:") || strings.HasPrefix(opt, "--fargate:") {
			launchFargate = true
		}

		switch opt {
		case "-p", "--profile":
			awsProfile = os.Args[i+1]
			i++
		case "-r", "--region":
			awsRegion = os.Args[i+1]
			i++
		case "-t", "--task-def", "--task-definition":
			taskDef = os.Args[i+1]
			i++
		case "-c", "--cluster":
			cluster = os.Args[i+1]
			i++
		case "-n", "--container-name":
			containerName = os.Args[i+1]
			i++
		case "--cpu":
			ival, ierr := strconv.ParseInt(os.Args[i+1], 10, 64)
			if ierr != nil {
				log.Fatalf("Invalid CPU value: %s", ierr)
			} else {
				cpu = ival
			}
			i++
		case "--mem":
			ival, ierr := strconv.ParseInt(os.Args[i+1], 10, 64)
			if ierr != nil {
				log.Fatalf("Invalid Memory value: %s", ierr)
			} else {
				memory = ival
			}
			i++
		case "--mem-res":
			ival, ierr := strconv.ParseInt(os.Args[i+1], 10, 64)
			if ierr != nil {
				log.Fatalf("Invalid Memory value: %s", ierr)
			} else {
				memoryReservation = ival
			}
			i++
		case "-e", "--env":
			val, err := ValidateEnv(os.Args[i+1])
			i++
			if err != nil {
				log.Fatal(err)
			} else {
				envOverrides = append(envOverrides, val)
			}
		case "--env-file":
			vals, err := ParseEnvFile(os.Args[i+1])
			i++
			if err != nil {
				log.Fatal(err)
			} else {
				envOverrides = append(envOverrides, vals...)
			}
		case "-x", "--dry-run":
			dryRun = !isNoOpt
		case "-l", "--stream-log":
			streamLog = !isNoOpt
		case "-w", "--wait":
			waitStopped = !isNoOpt
		case "-h", "--help":
			usage()
			os.Exit(0)
		case "--exec-role":
			execRoleArn = os.Args[i+1]
			i++
		case "--task-role":
			taskRoleArn = os.Args[i+1]
			i++
		case "--shell":
			noShell = isNoOpt
			if !isNoOpt {
				shellPrefix = os.Args[i+1]
				i++
			}
		case "-f", "--fargate":
			launchFargate = !isNoOpt
			parsed, filters := readFilterArgs(nil, os.Args[i+1:]...)
			anyFilters = append(anyFilters, filters...)
			i = i + parsed
		case "-f:sg", "--fargate:sg":
			doFilterSgs = !isNoOpt
			parsed, filters := readFilterArgs(aws.String(FilterTagName), os.Args[i+1:]...)
			vpcSgFilters = append(vpcSgFilters, filters...)
			i = i + parsed
		case "-f:vpc", "--fargate:vpc":
			doFilterVpc = !isNoOpt
			parsed, filters := readFilterArgs(aws.String(FilterTagName), os.Args[i+1:]...)
			vpcFilters = append(vpcFilters, filters...)
			i = i + parsed
		case "-f:ip", "--fargate:ip":
			netPublicIp = !isNoOpt
		case "-f:net", "--fargate:net":
			filterMode = FilterModeNetwork
			parsed, filters := readFilterArgs(aws.String(FilterTagName), os.Args[i+1:]...)
			vpcNetFilters = append(vpcNetFilters, filters...)
			i = i + parsed
		case "-f:host", "--fargate:host":
			filterMode = FilterModeHost
			parsed, filters := readFilterArgs(aws.String(FilterTagName), os.Args[i+1:]...)
			vpcHostFilters = append(vpcHostFilters, filters...)
			i = i + parsed
		case "--":
			overridesCmd = true
			cmdOverride = append(cmdOverride, os.Args[i+1:]...)
			break ArgLoop
		default:
			usage()
			log.Fatalf("Invalid option: \"%s\"", opt)
		}
	}

	return ParsedArgs{
		AwsProfile:        awsProfile,
		AwsRegion:         awsRegion,
		TaskDef:           taskDef,
		Cluster:           cluster,
		ContainerName:     containerName,
		DryRun:            dryRun,
		StreamLog:         streamLog,
		WaitStopped:       waitStopped,
		Cpu:               cpu,
		Memory:            memory,
		MemoryReservation: memoryReservation,
		Environment:       ConvertKVStringsToMap(envOverrides),
		ExecRoleArn:       execRoleArn,
		TaskRoleArn:       taskRoleArn,
		ShellPrefix:       shellPrefix,
		NoShell:           noShell,
		LaunchFargate:     launchFargate,
		FilterMode:        filterMode,
		AnyFilters:        anyFilters,
		VpcFilters:        vpcFilters,
		DoFilterVpc:       doFilterVpc,
		VpcSgFilters:      vpcSgFilters,
		DoFilterSgs:       doFilterSgs,
		VpcNetFilters:     vpcNetFilters,
		VpcHostFilters:    vpcHostFilters,
		NetPublicIp:       netPublicIp,
		OverridesCmd:      overridesCmd,
		CmdOverride:       cmdOverride}
}

func sigintStopTask(sigs chan os.Signal, s *ecs.ECS, taskArn *string, cluster *string) {
	// create the stop-task input before waiting on sigs, so that it is ready to send ASAP.
	stopInput := ecs.StopTaskInput{
		Cluster: cluster,
		Reason:  aws.String("overrun SIGINT"),
		Task:    taskArn}

SignalLoop:
	for {
		req := s.StopTaskRequest(&stopInput)

		sig, ok := <-sigs
		if !ok {
			break SignalLoop
		}

		if sig == syscall.SIGINT {
			if _, err := req.Send(); err != nil {
				// sigint
				log.Printf("ERROR: SIGINT failed to stop task %s! keep mashing that ctrl-c!\n", *taskArn)
			} else {
				// detach from SIGINT.
				signal.Stop(sigs)
				log.Printf("user requested to stop task %s using ctrl-c/SIGINT\n", *taskArn)
			}
		}
	}
}

func main() {
	prefs := parseArgs()

	if len(prefs.TaskDef) == 0 {
		log.Fatal("You must specify a --task-def.")
	}

	var awsCfg aws.Config
	if len(prefs.AwsProfile) > 0 {
		cfg, err := external.LoadDefaultAWSConfig(
			external.WithSharedConfigProfile(prefs.AwsProfile))
		if err != nil {
			log.Fatal(err)
		}
		awsCfg = cfg
	} else {
		cfg, err := external.LoadDefaultAWSConfig()
		if err != nil {
			log.Fatal(err)
		}
		awsCfg = cfg
	}

	if len(prefs.AwsRegion) > 0 {
		awsCfg.Region = prefs.AwsRegion
	}

	dtdInput := ecs.DescribeTaskDefinitionInput{TaskDefinition: &prefs.TaskDef}
	ecss := ecs.New(awsCfg)
	dtdResult, dtdErr := ecss.DescribeTaskDefinitionRequest(&dtdInput).Send()
	if dtdErr != nil {
		log.Fatal(dtdErr)
	}

	taskDefinition := dtdResult.TaskDefinition
	var containerDef *ecs.ContainerDefinition
	if len(prefs.ContainerName) == 0 {
		if len(taskDefinition.ContainerDefinitions) > 0 {
			containerDef = &dtdResult.TaskDefinition.ContainerDefinitions[0]
			prefs.ContainerName = *containerDef.Name
		} else {
			log.Fatalf("No container definitions found for task def %s\n", prefs.TaskDef)
		}
	} else {
		matched := false
		var availNames []string
		for _, contDef := range taskDefinition.ContainerDefinitions {
			availNames = append(availNames, *contDef.Name)
			if prefs.ContainerName == *contDef.Name {
				containerDef = &contDef
				matched = true
			}
		}
		if !matched {
			log.Fatalf("No container definition found with specified image name %s. Available names: %s\n", prefs.ContainerName, availNames)
		}
	}

	if containerDef == nil {
		log.Fatal("Failed to retrieve a container definition.")
	}

	if containerDef.LogConfiguration != nil {
		driver := (*containerDef.LogConfiguration).LogDriver
		if prefs.StreamLog && driver != ecs.LogDriverAwslogs {
			log.Printf("WARNING: Cannot stream logs for this log driver: %s\n", driver)
			prefs.StreamLog = false
		}
	}

	if len(prefs.Cluster) == 0 {
		log.Fatal("No --cluster specified. Specify 'default' to run on the the default cluster.")
	}

	ctx := ExecutionContext{
		AwsConfig:           &awsCfg,
		TaskDefinition:      taskDefinition,
		ContainerDefinition: containerDef,
		AnyFilters:          prefs.AnyFilters}

	if prefs.DryRun {
		log.Println("ANY Filters")
		for _, filter := range prefs.AnyFilters {
			log.Println(filter)
		}
		log.Println("VPC Filters")
		for _, filter := range prefs.VpcFilters {
			log.Println(filter)
		}
		log.Println("SG Filters")
		for _, filter := range prefs.VpcSgFilters {
			log.Println(filter)
		}
		log.Println("NET Filters")
		for _, filter := range prefs.VpcNetFilters {
			log.Println(filter)
		}
		log.Println("HOST Filters")
		for _, filter := range prefs.VpcHostFilters {
			log.Println(filter)
		}
	}

	runTaskInput, taskInputErr := buildRunTaskInput(&prefs, &ctx)
	if taskInputErr != nil {
		log.Fatal(taskInputErr)
	}

	if prefs.DryRun {
		log.Println(runTaskInput.String())
	} else {
		out, err := ecss.RunTaskRequest(runTaskInput).Send()
		if err != nil {
			log.Fatal(err)
		}

		task := out.Tasks[0]
		log.Printf("Submitted task %s on cluster %s.\n", *task.TaskArn, prefs.Cluster)
		taskArnInput := ecs.DescribeTasksInput{Cluster: &prefs.Cluster, Tasks: []string{*task.TaskArn}}

		if prefs.WaitStopped || prefs.StreamLog {

			runtime.GOMAXPROCS(3) // signal + log stream + wait stopped (main)

			// attach sigint handler to
			sigs := make(chan os.Signal, 1)
			go sigintStopTask(sigs, ecss, task.TaskArn, &prefs.Cluster)
			signal.Notify(sigs, syscall.SIGINT)

			if prefs.WaitStopped {
				err := ecss.WaitUntilTasksStopped(&taskArnInput)
				if err != nil {
					log.Fatal(err)
				}
			}

			if prefs.StreamLog {
				// extrapolate the cloudwatch stream name
				loc, locErr := LocateAwslogsForTask(containerDef, &task)
				if locErr != nil {
					log.Fatal(locErr)
				}

				// attempt to pre-create the log stream to avoid missing resource failures
				cws := cloudwatchlogs.New(*ctx.AwsConfig)
				_, streamErr := GetOrCreateStream(cws, loc)
				if streamErr != nil {
					log.Printf("WARNING: %s\n", streamErr)
				}

				// start paging events to standard out in separate thread.
				// use the wait group to notify when at least one getLogEvents
				// response has been received.
				var wg sync.WaitGroup
				wg.Add(1)
				go GoTailLogs(cws, loc, &wg)

				// wait for task to stop for good
				err := ecss.WaitUntilTasksStopped(&taskArnInput)
				if err != nil {
					log.Fatal(err)
				}

				// now wait for the GoTailLogs routine to notify completion of at least one filter-log-events request
				wg.Wait()

				// describe task final state to report reason and exit code of primary container
				describeResult, describeErr := ecss.DescribeTasksRequest(&taskArnInput).Send()
				if describeErr != nil {
					log.Fatal(describeErr)
				} else {
					finalTask := describeResult.Tasks[0]
					for _, cnt := range finalTask.Containers {
						if *cnt.Name == prefs.ContainerName {
							exitCode := 0
							if cnt.Reason != nil {
								exitCode = 42
								log.Println(*cnt.Reason)
							}
							if cnt.ExitCode != nil && int(*cnt.ExitCode) > 0 {
								os.Exit(int(*cnt.ExitCode))
							} else {
								os.Exit(exitCode)
							}
						}
					}
					log.Fatalln(finalTask.StoppedReason)
				}
			}
		}
	}
}

type ExecutionContext struct {
	AwsConfig           *aws.Config
	TaskDefinition      *ecs.TaskDefinition
	ContainerDefinition *ecs.ContainerDefinition
	AnyFilters          []ec2.Filter
}

func restrictToVpcs(prefs *ParsedArgs, ctx *ExecutionContext) (*ec2.Filter, error) {
	if prefs.DoFilterVpc {
		if len(prefs.VpcFilters) > 0 && *prefs.VpcFilters[0].Name == FilterVpcId {
			return &prefs.VpcFilters[0], nil
		}
		ec2s := ec2.New(*ctx.AwsConfig)
		input := ec2.DescribeVpcsInput{Filters: append(prefs.VpcFilters, ctx.AnyFilters...)}
		result, err := ec2s.DescribeVpcsRequest(&input).Send()
		if err != nil {
			return nil, err
		} else if len(result.Vpcs) > 0 {
			vpcs := make([]string, len(result.Vpcs))
			for i, vpc := range result.Vpcs {
				vpcs[i] = *vpc.VpcId
			}
			vpcsFilter := ec2.Filter{Name: aws.String(FilterVpcId), Values: vpcs}
			return &vpcsFilter, nil
		}
	}
	return nil, nil
}

func constructFargateVpcConfig(prefs *ParsedArgs, ctx *ExecutionContext) (ecs.NetworkConfiguration, error) {
	switch prefs.FilterMode {
	case FilterModeHost:
		return vpcConfigForHost(prefs, ctx, prefs.VpcHostFilters)
	case FilterModeNetwork:
		return vpcConfigForNet(prefs, ctx, prefs.VpcNetFilters)
	}
	return vpcConfigForCluster(prefs, ctx)
}

func secGroupsQuery(ctx *ExecutionContext, filters []ec2.Filter) ([]string, error) {
	ec2s := ec2.New(*ctx.AwsConfig)
	input := ec2.DescribeSecurityGroupsInput{Filters: append(filters, ctx.AnyFilters...)}

	result, err := ec2s.DescribeSecurityGroupsRequest(&input).Send()
	if err != nil {
		return nil, err
	} else {
		groupIds := make([]string, len(result.SecurityGroups))
		for i, group := range result.SecurityGroups {
			groupIds[i] = *group.GroupId
		}
		return groupIds, nil
	}
}

func vpcConfigForCluster(prefs *ParsedArgs, ctx *ExecutionContext) (ecs.NetworkConfiguration, error) {
	ecss := ecs.New(*ctx.AwsConfig)
	ciInput := ecs.ListContainerInstancesInput{Cluster: &prefs.Cluster}
	ciResult, ciErr := ecss.ListContainerInstancesRequest(&ciInput).Send()
	if ciErr != nil {
		return ecs.NetworkConfiguration{}, ciErr
	} else if len(ciResult.ContainerInstanceArns) > 0 {
		input := ecs.DescribeContainerInstancesInput{Cluster: &prefs.Cluster, ContainerInstances: ciResult.ContainerInstanceArns}
		result, err := ecss.DescribeContainerInstancesRequest(&input).Send()
		if err != nil {
			return ecs.NetworkConfiguration{}, err
		} else if len(result.ContainerInstances) > 0 {
			var instanceIds []string
			for _, ci := range result.ContainerInstances {
				if ci.Ec2InstanceId != nil {
					instanceIds = append(instanceIds, *ci.Ec2InstanceId)
				}
			}
			instanceFilter := ec2.Filter{Name: aws.String(FilterInstanceId), Values: instanceIds}
			return vpcConfigForHost(prefs, ctx, []ec2.Filter{instanceFilter})
		}
	}
	return ecs.NetworkConfiguration{}, errors.New(
		fmt.Sprintf("no describable container instances running in cluster %s. please specify --fargate:net or --fargate:host",
			prefs.Cluster))
}

func vpcConfigForNet(prefs *ParsedArgs, ctx *ExecutionContext, filters []ec2.Filter) (ecs.NetworkConfiguration, error) {
	ec2s := ec2.New(*ctx.AwsConfig)
	dsInput := ec2.DescribeSubnetsInput{}
	dsInput.Filters = filters
	dsInput.Filters = append(dsInput.Filters, ctx.AnyFilters...)

	dsResult, dsErr := ec2s.DescribeSubnetsRequest(&dsInput).Send()
	if dsErr != nil {
		log.Println(dsInput.Filters[0].String())
		return ecs.NetworkConfiguration{}, dsErr
	}

	if len(dsResult.Subnets) > 0 && dsResult.Subnets[0].VpcId != nil {
		var subnets []string
		vpcId := dsResult.Subnets[0].VpcId
		for i, subnet := range dsResult.Subnets {
			if i < 10 && *subnet.VpcId == *vpcId {
				subnets = append(subnets, *subnet.SubnetId)
			}
		}

		var sgroups []string
		sgResult, sgErr := secGroupsQuery(ctx, prefs.VpcSgFilters)
		if sgErr != nil {
			return ecs.NetworkConfiguration{}, sgErr
		} else {
			sgroups = append(sgroups, sgResult...)
		}
		if len(sgroups) > 10 {
			sgroups = sgroups[0:10]
		}

		assignPublicIp := ecs.AssignPublicIpDisabled
		if prefs.NetPublicIp {
			assignPublicIp = ecs.AssignPublicIpEnabled
		}

		awsvpc := ecs.AwsVpcConfiguration{Subnets: subnets, SecurityGroups: sgroups, AssignPublicIp: assignPublicIp}
		return ecs.NetworkConfiguration{AwsvpcConfiguration: &awsvpc}, nil
	} else {
		return ecs.NetworkConfiguration{}, errors.New("failed to find subnet matching filters: " + FilterString(filters))
	}
}

func vpcConfigForHost(prefs *ParsedArgs, ctx *ExecutionContext, filters []ec2.Filter) (ecs.NetworkConfiguration, error) {
	ec2s := ec2.New(*ctx.AwsConfig)
	diInput := ec2.DescribeInstancesInput{}
	diInput.Filters = filters
	diInput.Filters = append(diInput.Filters, ctx.AnyFilters...)

	diResult, diErr := ec2s.DescribeInstancesRequest(&diInput).Send()
	if diErr != nil {
		return ecs.NetworkConfiguration{}, diErr
	}

	if len(diResult.Reservations) > 0 && len(diResult.Reservations[0].Instances) > 0 {
		instance := diResult.Reservations[0].Instances[0]
		subnets := []string{*instance.SubnetId}

		sgroupMap := make(map[string]string, len(instance.SecurityGroups))
		for _, sgroup := range instance.SecurityGroups {
			id := *sgroup.GroupId
			sgroupMap[id] = id
		}
		if prefs.DoFilterSgs {
			sgResult, sgErr := secGroupsQuery(ctx, prefs.VpcSgFilters)
			if sgErr != nil {
				return ecs.NetworkConfiguration{}, sgErr
			} else {
				for _, id := range sgResult {
					if len(sgroupMap) < 10 {
						sgroupMap[id] = id
					}
				}
			}
		}
		assignPublicIp := ecs.AssignPublicIpDisabled
		if prefs.NetPublicIp {
			assignPublicIp = ecs.AssignPublicIpEnabled
		}
		var sgroups []string
		for _, v := range sgroupMap {
			if len(sgroups) < 10 {
				sgroups = append(sgroups, v)
			} else {
				break
			}
		}
		awsvpc := ecs.AwsVpcConfiguration{Subnets: subnets, SecurityGroups: sgroups, AssignPublicIp: assignPublicIp}
		return ecs.NetworkConfiguration{AwsvpcConfiguration: &awsvpc}, nil
	} else {
		return ecs.NetworkConfiguration{}, errors.New("failed to find instance matching filter: " + FilterString(filters))
	}
}

func constructCommand(prefs *ParsedArgs) []string {
	if prefs.NoShell {
		return prefs.CmdOverride
	} else {
		escaped := make([]string, len(prefs.CmdOverride))
		for i, arg := range prefs.CmdOverride {
			if strings.ContainsRune(arg, ' ') {
				arg = fmt.Sprintf("\"%s\"", strings.Replace(arg, "\"", "\\\"", -1))
			}
			escaped[i] = arg
		}
		escapedStr := strings.Join(escaped, " ")
		if len(prefs.ShellPrefix) > 0 && prefs.ShellPrefix != " " {
			return []string{fmt.Sprintf("%s '%s'", prefs.ShellPrefix,
				strings.Replace(escapedStr, "'", "'\"'\"'", -1))}
		} else {
			return []string{escapedStr}
		}
	}
}

func buildOverrides(prefs *ParsedArgs) *ecs.TaskOverride {
	tsk := ecs.TaskOverride{}
	if len(prefs.ExecRoleArn) > 0 {
		tsk.ExecutionRoleArn = &prefs.ExecRoleArn
	}
	if len(prefs.TaskRoleArn) > 0 {
		tsk.TaskRoleArn = &prefs.ExecRoleArn
	}

	cnt := ecs.ContainerOverride{Name: &prefs.ContainerName}
	if prefs.OverridesCmd {
		cnt.Command = constructCommand(prefs)
	}

	for key, val := range prefs.Environment {
		cnt.Environment = append(cnt.Environment, ecs.KeyValuePair{Name: &key, Value: &val})
	}

	if prefs.Cpu > int64(0) {
		cnt.Cpu = &prefs.Cpu
	}
	if prefs.Memory > int64(0) {
		cnt.Memory = &prefs.Memory
	}
	if prefs.MemoryReservation > int64(0) {
		cnt.MemoryReservation = &prefs.MemoryReservation
	}

	tsk.ContainerOverrides = []ecs.ContainerOverride{cnt}
	return &tsk
}

func buildRunTaskInput(prefs *ParsedArgs, ctx *ExecutionContext) (*ecs.RunTaskInput, error) {
	input := ecs.RunTaskInput{}
	input.Cluster = &prefs.Cluster
	input.TaskDefinition = &prefs.TaskDef

	if prefs.LaunchFargate {
		vpcsFilter, err := restrictToVpcs(prefs, ctx)
		if err != nil {
			return nil, err
		}

		if vpcsFilter != nil {
			ctx.AnyFilters = append(ctx.AnyFilters, *vpcsFilter)
		}

		netConfig, err := constructFargateVpcConfig(prefs, ctx)
		if err != nil {
			return nil, err
		}
		input.LaunchType = ecs.LaunchTypeFargate
		input.NetworkConfiguration = &netConfig
	} else {
		input.LaunchType = ecs.LaunchTypeEc2
	}

	input.Overrides = buildOverrides(prefs)
	return &input, nil
}
