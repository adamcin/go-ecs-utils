package main

import (
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/external"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type ParsedArgs struct {
	// pass-through profile and region args to aws sdk
	AwsProfile, AwsRegion string

	// get, put, delete, clear
	SsmCmd string

	//
	ConfDir string

	KeyIdPutAll string

	OverwritePut bool

	ClearOnPut bool

	NoStoreSecureString bool

	NoPutSecureString bool

	Filenames []string

	Prefixes []string
}

const NoOptPrefix = "--no-"

func parseArgs() ParsedArgs {
	awsProfile := ""
	awsRegion := ""
	ssmCmd := ""
	rawConfDir := "."
	_, cwdErr := os.Getwd()
	if cwdErr != nil {
		log.Fatal("Failed to get current working directory")
	}

	filenames := make([]string, 0)
	prefixes := make([]string, 0)

	keyIdPutAll := ""
	overwritePut := false
	clearOnPut := false
	noStoreSecureString := false
	noPutSecureString := false
	isHelp := false

	for i := 1; i < len(os.Args); i++ {
		opt := os.Args[i]
		isNoOpt := strings.HasPrefix(opt, NoOptPrefix)
		if isNoOpt {
			opt = "--" + strings.TrimPrefix(opt, NoOptPrefix)
		}

		switch opt {
		case "-h", "--help":
			isHelp = true
		case "-p", "--profile":
			awsProfile = os.Args[i+1]
			i++
		case "-r", "--region":
			awsRegion = os.Args[i+1]
			i++
		case "-C", "--conf-dir":
			rawConfDir = os.Args[i+1]
			i++
		case "-f", "--filename":
			filenames = append(filenames, os.Args[i+1])
			i++
		case "-s", "--starts-with":
			prefixes = append(prefixes, os.Args[i+1])
			i++
		case "-k", "--key-id-put-all":
			keyIdPutAll = os.Args[i+1]
			i++
		case "-o", "--overwrite-put":
			overwritePut = !isNoOpt
		case "--clear-on-put":
			clearOnPut = !isNoOpt
		case "--store-secure-string":
			noStoreSecureString = isNoOpt
		case "--put-secure-string":
			noPutSecureString = isNoOpt
		case "get", "put", "delete", "clear":
			ssmCmd = opt
		default:
			usage(ssmCmd)
			log.Fatal(fmt.Sprintf("Unrecognized option %s", opt))
		}
	}

	if isHelp {
		usage(ssmCmd)
		os.Exit(0)
	}

	if len(ssmCmd) == 0 {
		usage(ssmCmd)
		os.Exit(1)
	}

	confDir, confErr := filepath.Abs(rawConfDir)
	if confErr != nil {
		log.Fatal("Failed to resolve confDir "+rawConfDir, confErr)
	}

	if len(prefixes) == 0 {
		log.Fatal("At least one -s/--starts-with path is required, like /ecs/dev/myapp")
	}

	if len(filenames) == 0 {
		log.Fatal("At least one -f/--filename argument is required, like instance.properties")
	}

	return ParsedArgs{
		AwsProfile:          awsProfile,
		AwsRegion:           awsRegion,
		SsmCmd:              ssmCmd,
		ConfDir:             confDir,
		Filenames:           filenames,
		Prefixes:            prefixes,
		KeyIdPutAll:         keyIdPutAll,
		OverwritePut:        overwritePut,
		ClearOnPut:          clearOnPut,
		NoStoreSecureString: noStoreSecureString,
		NoPutSecureString:   noPutSecureString}
}

func main() {
	prefs := parseArgs()

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

	execCmd(prefs, awsCfg)
}

func execCmd(prefs ParsedArgs, cfg aws.Config) {
	ssms := ssm.New(cfg)
	kmss := kms.New(cfg)

	fileStores := make(map[string]*FileStore, len(prefs.Filenames))
	for _, fn := range prefs.Filenames {
		fs := NewFileStore(prefs.ConfDir, fn)
		if err := fs.Load(); err != nil {
			log.Fatalf("Failed to load file store for name %s. reason: %s", fn, err)
		}
		fileStores[fn] = &fs
	}

	kmsMap := KmsMap{
		aliasesToKeys: make(map[string]string, 0),
		keysToAliases: make(map[string]string, 0)}

	ctx := CmdContext{
		Prefs:  prefs,
		Stores: fileStores,
		Ssms:   ssms,
		KmsMap: kmsMap}

	switch strings.ToLower(prefs.SsmCmd) {
	case "get":
		if !prefs.NoStoreSecureString {
			buildAliasList(kmss, &kmsMap)
		}
		doGet(&ctx)
	case "put":
		if !prefs.NoPutSecureString {
			buildAliasList(kmss, &kmsMap)
		}
		doPut(&ctx)
	case "delete":
		doDelete(&ctx)
	case "clear":
		doClear(&ctx)
	default:
		log.Fatalf("Unknown command %s", prefs.SsmCmd)
	}
}
