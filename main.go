package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-utils/command"
	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/log"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-tools/go-steputils/stepconf"
)

// ConfigsModel ...
type ConfigsModel struct {
	Configuration string `env:"configuration,required"`

	DevelopmentTeam     string `env:"development_team"`
	CodeSignIdentity    string `env:"code_sign_identity"`
	ProvisioningProfile string `env:"provisioning_profile"`
	PackageType         string `env:"package_type"`

	KeystoreURL        string          `env:"keystore_url"`
	KeystorePassword   stepconf.Secret `env:"keystore_password"`
	KeystoreAlias      string          `env:"keystore_alias"`
	PrivateKeyPassword stepconf.Secret `env:"private_key_password"`
}

// IOSBuildConfigurationItem ...
type IOSBuildConfigurationItem struct {
	CodeSignIdentity    string `json:"codeSignIdentity,omitempty"`
	ProvisioningProfile string `json:"provisioningProfile,omitempty"`
	DevelopmentTeam     string `json:"developmentTeam,omitempty"`
	PackageType         string `json:"packageType,omitempty"`
}

// AndroidBuildConfigurationItem ...
type AndroidBuildConfigurationItem struct {
	Keystore      string `json:"keystore,omitempty"`
	StorePassword string `json:"storePassword,omitempty"`
	Alias         string `json:"alias,omitempty"`
	Password      string `json:"password,omitempty"`
}

// BuildConfiguration ...
type BuildConfiguration struct {
	Android map[string]AndroidBuildConfigurationItem `json:"android,omitempty"`
	IOS     map[string]IOSBuildConfigurationItem     `json:"ios,omitempty"`
}

func download(url, pth string) error {
	out, err := os.Create(pth)
	defer func() {
		if err := out.Close(); err != nil {
			log.Warnf("Failed to close file: %s, error: %s", out, err)
		}
	}()

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warnf("Failed to close response body, error: %s", err)
		}
	}()

	_, err = io.Copy(out, resp.Body)
	return err
}

func exportEnvironmentWithEnvman(keyStr, valueStr string) error {
	cmd := command.New("envman", "add", "--key", keyStr)
	cmd.SetStdin(strings.NewReader(valueStr))
	return cmd.Run()
}

func fail(format string, v ...interface{}) {
	log.Errorf(format, v...)
	os.Exit(1)
}

func main() {
	var configs ConfigsModel
	if err := stepconf.Parse(&configs); err != nil {
		fail("Couldn't create config: %v\n", err)
	}
	stepconf.Print(configs)

	buildConfig := BuildConfiguration{}

	tmpDir, err := pathutil.NormalizedOSTempDirPath("__bitrise-cordova-build-config__")
	if err != nil {
		fail("Failed to create tmp dir, error: %s", err)
	}

	fmt.Println()

	// Android Build Config
	if configs.KeystoreURL != "" {
		log.Infof("Adding android build config")

		keystorePath := ""
		if strings.HasPrefix(configs.KeystoreURL, "file://") {
			rawPth := strings.TrimPrefix(configs.KeystoreURL, "file://")
			absPth, err := pathutil.AbsPath(rawPth)
			if err != nil {
				fail("Failed to expand path (%s), error: %s", rawPth, err)
			}
			keystorePath = absPth
		} else {
			log.Printf("download keystore")

			keystorePath = path.Join(tmpDir, "keystore.jks")
			if err := download(configs.KeystoreURL, keystorePath); err != nil {
				fail("Failed to download keystore, error: %s", err)
			}
		}

		androidBuildConfig := AndroidBuildConfigurationItem{
			Keystore:      keystorePath,
			StorePassword: string(configs.KeystorePassword),
			Alias:         configs.KeystoreAlias,
			Password:      string(configs.PrivateKeyPassword),
		}

		buildConfig.Android = map[string]AndroidBuildConfigurationItem{
			configs.Configuration: androidBuildConfig,
		}
	}

	// iOS Build Config
	if configs.PackageType != "none" {
		log.Infof("Adding ios build config")

		iosBuildConfig := IOSBuildConfigurationItem{
			CodeSignIdentity:    configs.CodeSignIdentity,
			ProvisioningProfile: configs.ProvisioningProfile,
			DevelopmentTeam:     configs.DevelopmentTeam,
			PackageType:         configs.PackageType,
		}

		buildConfig.IOS = map[string]IOSBuildConfigurationItem{
			configs.Configuration: iosBuildConfig,
		}
	}

	if len(buildConfig.Android) == 0 && len(buildConfig.IOS) == 0 {
		log.Warnf("No ios nor android build config parameters specified, nothing to generate...")
		os.Exit(0)
	}

	// Generating build.json
	fmt.Println()
	log.Infof("Generating config file")

	buildConfigBytes, err := json.MarshalIndent(buildConfig, "", "  ")
	if err != nil {
		fail("Failed to marshal build config, error: %s", err)
	}

	{
		printableConfigBytes := append([]byte{}, buildConfigBytes...)

		if config, ok := buildConfig.Android[configs.Configuration]; ok {
			if config.Password != "" {
				config.Password = "*****"
			}
			if config.StorePassword != "" {
				config.StorePassword = "*****"
			}

			buildConfig.Android[configs.Configuration] = config

			printableConfigBytes, err = json.MarshalIndent(buildConfig, "", "  ")
			if err != nil {
				fail("Failed to marshal build config, error: %s", err)
			}
		}

		log.Printf("content:")
		log.Printf(string(printableConfigBytes))
	}

	buildConfigPth := filepath.Join(tmpDir, "build.json")
	if err := fileutil.WriteBytesToFile(buildConfigPth, buildConfigBytes); err != nil {
		fail("Failed to write build.json file, error: %s", err)
	}

	if err := exportEnvironmentWithEnvman("BITRISE_CORDOVA_BUILD_CONFIGURATION", buildConfigPth); err != nil {
		fail("Failed to export BITRISE_CORDOVA_BUILD_CONFIGURATION, error: %s", err)
	}
	log.Donef("The build.json path is now available in the Environment Variable: BITRISE_CORDOVA_BUILD_CONFIGURATION (value: %s)", buildConfigPth)
}
