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
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"regexp"
	"strings"
)

const FilterInstanceId = "instance-id"
const FilterSubnetId = "subnet-id"
const FilterSecurityGroupId = "group-id"
const FilterVpcId = "vpc-id"
const FilterTagName = "tag:Name"

const MatchInstanceId = "^i-"
const MatchSubnetId = "^subnet-"
const MatchSecurityGroupId = "^sg-"
const MatchVpcId = "^vpc-"
const MatchShortFilter = "^[^=]+=.*$"
const MatchLongFilter = "^Name=([^,]+),Values=(.*)$"

func ParseEc2Filter(filter string, defaultFilter *string) (bool, ec2.Filter) {
	longPat := regexp.MustCompile(MatchLongFilter)
	if longPat.MatchString(filter) {
		subs := longPat.FindStringSubmatch(filter)
		name := subs[1]
		vals := strings.Split(subs[2], ",")
		return true, ec2.Filter{Name: &name, Values: vals}
	} else if matches, _ := regexp.MatchString(MatchShortFilter, filter); matches {
		subs := strings.SplitN(filter, "=", 2)
		name := subs[0]
		vals := strings.Split(subs[1], ",")
		return true, ec2.Filter{Name: &name, Values: vals}
	} else if strings.HasPrefix(filter, "-") {
		return false, ec2.Filter{}
	} else if matches, _ := regexp.MatchString(MatchInstanceId, filter); matches {
		name := FilterInstanceId
		return true, ec2.Filter{Name: &name, Values: []string{filter}}
	} else if matches, _ := regexp.MatchString(MatchSubnetId, filter); matches {
		name := FilterSubnetId
		return true, ec2.Filter{Name: &name, Values: []string{filter}}
	} else if matches, _ := regexp.MatchString(MatchVpcId, filter); matches {
		name := FilterVpcId
		return true, ec2.Filter{Name: &name, Values: []string{filter}}
	} else if matches, _ := regexp.MatchString(MatchSecurityGroupId, filter); matches {
		name := FilterSecurityGroupId
		return true, ec2.Filter{Name: &name, Values: []string{filter}}
	} else if defaultFilter != nil {
		name := *defaultFilter
		return true, ec2.Filter{Name: &name, Values: []string{filter}}
	} else {
		return false, ec2.Filter{}
	}
}

func FilterString(filters []ec2.Filter) string {
	filterStrings := make([]string, len(filters))
	for i, f := range filters {
		filterStrings[i] = f.String()
	}
	return strings.Join(filterStrings, " ")
}
