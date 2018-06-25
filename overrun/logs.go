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
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/hashicorp/golang-lru"
	"log"
	"strings"
	"sync"
)

type AwslogsLocation struct {
	LogGroupName  *string
	LogStreamName *string
}

const AwslogsKeyGroup = "awslogs-group"
const AwslogsKeyStreamPrefix = "awslogs-stream-prefix"

func LocateAwslogsForTask(definition *ecs.ContainerDefinition, forTask *ecs.Task) (*AwslogsLocation, error) {
	if definition != nil && definition.LogConfiguration.LogDriver == ecs.LogDriverAwslogs {
		input := AwslogsLocation{}
		options := definition.LogConfiguration.Options

		if group, ok := options[AwslogsKeyGroup]; ok {
			input.LogGroupName = &group
		} else {
			return nil, errors.New("container definition log options does not contain key " + AwslogsKeyGroup)
		}

		prefix, ok := options[AwslogsKeyStreamPrefix]
		if !ok {
			return nil, errors.New("log streaming requires the container definition to define the " + AwslogsKeyStreamPrefix)
		}

		if forTask == nil || forTask.TaskArn == nil {
			return nil, errors.New("failed to locate log stream without task arn")
		}

		arnParts := strings.Split(*forTask.TaskArn, "/")
		taskId := arnParts[len(arnParts)-1]

		if definition.Name == nil {
			return nil, errors.New("failed to locate log stream without container name")
		}

		streamName := fmt.Sprintf("%s/%s/%s", prefix, *definition.Name, taskId)
		input.LogStreamName = &streamName

		return &input, nil
	}
	return nil, errors.New("no awslog stream available")
}

func ErrorIsAlreadyExists(err error) bool {
	return strings.HasPrefix(err.Error(), cloudwatchlogs.ErrCodeResourceAlreadyExistsException)
}

func ErrorIsResourceNotFound(err error) bool {
	return strings.HasPrefix(err.Error(), cloudwatchlogs.ErrCodeResourceNotFoundException)
}

func GetOrCreateStream(cws *cloudwatchlogs.CloudWatchLogs, loc *AwslogsLocation) (*cloudwatchlogs.LogStream, error) {
	clgInput := cloudwatchlogs.CreateLogGroupInput{
		LogGroupName: loc.LogGroupName}
	if _, err := cws.CreateLogGroupRequest(&clgInput).Send(); err != nil && !ErrorIsAlreadyExists(err) {
		return nil, err
	}

	clsInput := cloudwatchlogs.CreateLogStreamInput{
		LogGroupName:  loc.LogGroupName,
		LogStreamName: loc.LogStreamName}
	if _, err := cws.CreateLogStreamRequest(&clsInput).Send(); err != nil && !ErrorIsAlreadyExists(err) {
		return nil, err
	}

	logInput := cloudwatchlogs.DescribeLogStreamsInput{}
	logInput.LogGroupName = loc.LogGroupName
	logInput.LogStreamNamePrefix = loc.LogStreamName

	result, err := cws.DescribeLogStreamsRequest(&logInput).Send()
	if err != nil {
		return nil, err
	} else if len(result.LogStreams) > 0 {
		return &result.LogStreams[0], nil
	} else {
		return nil, errors.New("failed to establish log stream")
	}
}

func GoTailLogs(s *cloudwatchlogs.CloudWatchLogs, l *AwslogsLocation, group *sync.WaitGroup) {
	cache, _ := lru.New(10000)
	firstRun := true
	startTime := int64(0)

	for {
		flInput := cloudwatchlogs.FilterLogEventsInput{
			LogGroupName:   l.LogGroupName,
			LogStreamNames: []string{*l.LogStreamName},
			StartTime:      &startTime}

		eventsRequest := s.FilterLogEventsRequest(&flInput)
		events := (&eventsRequest).Paginate()
		for events.Next() {
			eventsPage := events.CurrentPage()
			for _, event := range eventsPage.Events {
				if event.EventId == nil {
					continue
				}
				if ok, _ := cache.ContainsOrAdd(*event.EventId, *event.EventId); !ok {
					fmt.Println(*event.Message)
					if *event.Timestamp > startTime {
						startTime = *event.Timestamp
					}
				}
			}
		}

		if events.Err() != nil {
			if !ErrorIsResourceNotFound(events.Err()) {
				log.Printf("WARNING: log stream error: %s\n", events.Err())
			}
		} else if firstRun {
			firstRun = false
			group.Done()
		}
	}
}
