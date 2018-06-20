package main

import (
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"strings"
)

type KmsMap struct {
	aliasesToKeys map[string]string
	keysToAliases map[string]string
}

func (ka KmsMap) deref(alias string) string {
	var fqAlias string

	if strings.HasPrefix(alias, "alias/") {
		fqAlias = alias
	} else {
		fqAlias = "alias/" + alias
	}

	if val, ok := ka.aliasesToKeys[fqAlias]; ok {
		return val
	} else {
		return fqAlias
	}
}

func (ka KmsMap) aliasFor(keyId string) string {
	if val, ok := ka.keysToAliases[keyId]; ok {
		return val
	} else {
		return keyId
	}
}

func buildAliasList(kmss *kms.KMS, kmsMap *KmsMap) error {
	request := kmss.ListAliasesRequest(nil)
	result, err := request.Send()
	if err != nil {
		return err
	} else {
		for _, entry := range result.Aliases {
			if entry.TargetKeyId != nil && entry.AliasName != nil {
				kmsMap.aliasesToKeys[*entry.AliasName] = *entry.TargetKeyId
				kmsMap.keysToAliases[*entry.TargetKeyId] = *entry.AliasName
			}
		}
		return nil
	}
}
