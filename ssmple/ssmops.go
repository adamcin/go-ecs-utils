package main

import (
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"strings"
)

const KeyIdSuffix = "_SecureStringKeyId"

func findAllParametersForPath(ctx *CmdContext, paramPath string) ([]ssm.Parameter, error) {
	var paramsForPath []ssm.Parameter
	maxResults := int64(10)
	recursive := false
	withDecryption := true

	findParametersForPath := func(nextToken *string) (*string, error) {
		input := ssm.GetParametersByPathInput{
			MaxResults:     &maxResults,
			Path:           &paramPath,
			WithDecryption: &withDecryption,
			NextToken:      nextToken,
			Recursive:      &recursive}

		result, err := ctx.Ssms.GetParametersByPathRequest(&input).Send()
		if err != nil {
			return nil, err
		}

		if len(result.Parameters) > 0 {
			paramsForPath = append(paramsForPath, result.Parameters...)
			return result.NextToken, nil
		} else {
			return nil, nil
		}
	}

	token, err := findParametersForPath(nil)
	for ; token != nil && err == nil; token, err = findParametersForPath(token) {
		// iterate until no more next tokens or error
	}
	if err != nil {
		return nil, err
	} else {
		return paramsForPath, nil
	}
}

// If value is all spaces, subtract a space to reconstruct the original value for export.
func unescapeValueAfterGet(value string) string {
	if len(value) == 0 {
		return value
	}

	runes := []rune(value)
	for _, ru := range runes {
		if ru != rune(' ') {
			return value
		}
	}
	return string(runes[0 : len(runes)-1])
}

// If value is the empty string or all spaces, add a space so the value is non-empty for SSM.
func escapeValueBeforePut(value string) string {
	runes := []rune(value)
	for _, ru := range runes {
		if ru != rune(' ') {
			return value
		}
	}

	return value + " "
}

func getParamsPerPath(ctx *CmdContext, paramPath string, storeDict *map[string]string) error {
	filterKey, _ := ssm.ParametersFilterKeyName.MarshalValue()
	filterOption := "Equals"
	paramsForPath, findErr := findAllParametersForPath(ctx, paramPath)
	if findErr != nil {
		return findErr
	}

	for _, param := range paramsForPath {
		name := *param.Name

		if param.Type == ssm.ParameterTypeStringList ||
			(ctx.Prefs.NoStoreSecureString && param.Type == ssm.ParameterTypeSecureString) {
			continue
		}

		if !strings.HasPrefix(name, paramPath+"/") {
			continue
		}

		storeKey := strings.TrimPrefix(name, paramPath+"/")
		(*storeDict)[storeKey] = unescapeValueAfterGet(*param.Value)

		if param.Type == ssm.ParameterTypeSecureString {
			sidecarStoreKey := storeKey + KeyIdSuffix
			input := ssm.DescribeParametersInput{}
			input.ParameterFilters = append(input.ParameterFilters,
				ssm.ParameterStringFilter{
					Key:    &filterKey,
					Option: &filterOption,
					Values: []string{name}})

			result, err := ctx.Ssms.DescribeParametersRequest(&input).Send()
			if err != nil {
				return err
			}

			if len(result.Parameters) > 0 {
				if result.Parameters[0].KeyId != nil {
					(*storeDict)[sidecarStoreKey] = ctx.KmsMap.aliasFor(*result.Parameters[0].KeyId)
				}
			}
		}
	}
	return nil
}

// Build an SSM parameter path or name.
// prefix:   hierarchy levels 0-(N-2)
// filename: hierarchy level N-1 (.properties, .json, or .yaml extensions will be stripped)
// key:  	 optional, hierarchy level N
func buildParameterPath(prefix string, filename string, key string) string {
	sb := prefix
	if !strings.HasSuffix(sb, "/") {
		sb += "/"
	}
	if len(filename) == 0 {
		sb += "$"
	} else if strings.ContainsRune(filename, '.') {
		fnRunes := []rune(filename)
		sb += string(fnRunes[0:strings.LastIndex(filename, ".")])
	} else {
		sb += filename
	}
	if len(key) > 0 {
		if !strings.HasSuffix(sb, "/") {
			sb += "/"
		}
		sb += key
	}
	return sb
}

func getParamsPerFile(ctx *CmdContext, filename string) error {
	prefixes := ctx.Prefs.Prefixes
	store := ctx.Stores[filename]
	for _, prefix := range prefixes {
		paramPath := buildParameterPath(prefix, filename, "")
		if err := getParamsPerPath(ctx, paramPath, &store.Dict); err != nil {
			return err
		}
	}

	if len(store.Dict) > 0 {
		return store.Save()
	}

	return nil
}

func clearParamsPerFile(ctx *CmdContext, filename string, prefix string) error {
	paramPath := buildParameterPath(prefix, filename, "")
	params, findErr := findAllParametersForPath(ctx, paramPath)
	if findErr != nil {
		return findErr
	}
	count := len(params)
	names := make([]string, count)
	i := 0
	for _, param := range params {
		names[i] = *param.Name
		i++
	}

	batchSize := 10
	batches := (count / batchSize) + 1
	for b := 0; b < batches; b++ {
		input := ssm.DeleteParametersInput{}
		if b+1 < batches {
			input.Names = append(input.Names, names[batchSize*b:batchSize*(b+1)]...)
		} else {
			input.Names = append(input.Names, names[batchSize*b:]...)
		}
		if len(input.Names) > 0 {
			if _, err := ctx.Ssms.DeleteParametersRequest(&input).Send(); err != nil {
				return err
			}
		}
	}

	return nil
}

func putParamsPerFile(ctx *CmdContext, filename string, prefix string) error {
	if ctx.Prefs.ClearOnPut {
		if err := clearParamsPerFile(ctx, filename, prefix); err != nil {
			return err
		}
	}

	store := ctx.Stores[filename]
	for key, value := range store.Dict {
		if strings.HasSuffix(key, KeyIdSuffix) {
			continue
		}
		sidecarKeyId := key + KeyIdSuffix
		name := buildParameterPath(prefix, filename, key)

		keyId, isSecure := store.Dict[sidecarKeyId]
		if isSecure && ctx.Prefs.NoPutSecureString {
			continue
		}

		if len(ctx.Prefs.KeyIdPutAll) > 0 {
			isSecure = true
			keyId = ctx.Prefs.KeyIdPutAll
		}

		keyId = ctx.KmsMap.deref(keyId)

		escaped := escapeValueBeforePut(value)
		input := ssm.PutParameterInput{}

		input.Name = &name
		input.Value = &escaped
		input.Overwrite = &ctx.Prefs.OverwritePut

		if isSecure {
			input.KeyId = &keyId
			input.Type = ssm.ParameterTypeSecureString
		} else {
			input.Type = ssm.ParameterTypeString
		}

		_, err := ctx.Ssms.PutParameterRequest(&input).Send()
		if err != nil {
			return err
		}
	}

	return nil
}

func deleteParamsPerFile(ctx *CmdContext, filename string, prefix string) error {
	var names []string
	store := ctx.Stores[filename]
	for key := range store.Dict {
		names = append(names, buildParameterPath(prefix, filename, key))
	}

	paramPath := buildParameterPath(prefix, filename, "")
	allParams, findErr := findAllParametersForPath(ctx, paramPath)
	if findErr != nil {
		return findErr
	}

	var allNames []string
	for _, param := range allParams {
		allNames = append(allNames, *param.Name)
	}

	var toDelete []string
	for _, cand := range names {
		for _, name := range allNames {
			if cand == name {
				toDelete = append(toDelete, cand)
				break
			}
		}
	}

	count := len(toDelete)
	batchSize := 10
	batches := (count / batchSize) + 1
	for b := 0; b < batches; b++ {
		input := ssm.DeleteParametersInput{}
		if b+1 < batches {
			input.Names = append(input.Names, names[batchSize*b:batchSize*(b+1)]...)
		} else {
			input.Names = append(input.Names, names[batchSize*b:]...)
		}
		if len(input.Names) > 0 {
			if _, err := ctx.Ssms.DeleteParametersRequest(&input).Send(); err != nil {
				return err
			}
		}
	}

	return nil
}
