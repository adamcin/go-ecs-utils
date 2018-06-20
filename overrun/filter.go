package main

import (
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"regexp"
	"strings"
)

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
		name := subs[0]
		vals := strings.Split(subs[1], ",")
		return true, ec2.Filter{Name: &name, Values: vals}
	} else if matches, _ := regexp.MatchString(MatchShortFilter, filter); matches {
		subs := strings.SplitN(filter, "=", 2)
		name := subs[0]
		vals := strings.Split(subs[1], ",")
		return true, ec2.Filter{Name: &name, Values: vals}
	} else if strings.HasPrefix(filter, "-") {
		return false, ec2.Filter{}
	} else if matches, _ := regexp.MatchString(MatchInstanceId, filter); matches {
		name := "instance-id"
		return true, ec2.Filter{Name: &name, Values: []string{filter}}
	} else if matches, _ := regexp.MatchString(MatchSubnetId, filter); matches {
		name := "subnet-id"
		return true, ec2.Filter{Name: &name, Values: []string{filter}}
	} else if matches, _ := regexp.MatchString(MatchVpcId, filter); matches {
		name := "vpc-id"
		return true, ec2.Filter{Name: &name, Values: []string{filter}}
	} else if matches, _ := regexp.MatchString(MatchSecurityGroupId, filter); matches {
		name := "security-id"
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
