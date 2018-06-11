package hooks

import (
	"github.com/cloudfoundry/libbuildpack"
	"os"
	"net/http"
	"errors"
	"io"
	"encoding/json"
	"regexp"
	"path/filepath"
	"io/ioutil"
	"strconv"
	"fmt"
	"path"
	"net/url"
)

type SeekerAfterCompileHook struct {
	libbuildpack.DefaultHook
	Log                *libbuildpack.Logger
	serviceCredentials *SeekerCredentials
	Command            Command
}

type SeekerCredentials struct {
	SensorHost       string
	SensorPort       string
	EnterpriseServerURL string
}

func init() {
	logger := libbuildpack.NewLogger(os.Stdout)
	command := &libbuildpack.Command{}
	libbuildpack.AddHook(&SeekerAfterCompileHook{Log: logger, Command: command})
}

func (h SeekerAfterCompileHook) AfterCompile(compiler *libbuildpack.Stager) error {
	compiler.Logger().Debug("Seeker - AfterCompileHook Start")
	serviceCredentials, extractErrors := extractServiceCredentials(h.Log)
	if extractErrors != nil {
		h.Log.Error(extractErrors.Error())
		return nil
	}
	err := assertServiceCredentialsValid(serviceCredentials)
	if err != nil {
		return err
	}
	h.serviceCredentials = &serviceCredentials
	credentialsJSON, _ := json.Marshal(h.serviceCredentials)
	h.Log.Info("Credentials extraction ok: %s", credentialsJSON)
	err, seekerLibraryToInstall := h.fetchSeekerAgentTarball(compiler)
	if err == nil {
		h.Log.Info("Before Installing seeker agent dependency")
		h.updateNodeModules(seekerLibraryToInstall, compiler.BuildDir())
		h.Log.Info("After Installing seeker agent dependency")
		envVarsError := h.createSeekerEnvironmentScript(compiler)
		if envVarsError != nil {
			err = errors.New("Error creating seeker-env.sh script: " + envVarsError.Error())
		} else {
			h.Log.Info("Done creating seeker-env.sh script")
		}
	}
	return err

}
func assertServiceCredentialsValid(credentials SeekerCredentials) error {
	errorFormat := "mandatory `%s` is missing in Seeker service configuration"
	if credentials.SensorPort == "" {
		return errors.New(fmt.Sprintf(errorFormat, "sensor_port"))
	}
	if credentials.SensorHost == "" {
		return errors.New(fmt.Sprintf(errorFormat, "sensor_host"))
	}
	if credentials.EnterpriseServerURL == "" {
		return errors.New(fmt.Sprintf(errorFormat, "enterprise_server_url"))
	}
	return nil
}

func (h SeekerAfterCompileHook) downloadFile(url, destFile string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return errors.New("could not download: " + strconv.Itoa(resp.StatusCode))
	}
	return writeToFile(resp.Body, destFile, 0666)
}
func (h SeekerAfterCompileHook) fetchSeekerAgentTarball(compiler *libbuildpack.Stager) (error, string) {
	var sensorDownloadRelativeUrl = "rest/ui/installers/binaries/LINUX"
	parsedEnterpriseServerUrl, err := url.Parse(h.serviceCredentials.EnterpriseServerURL)
	if err != nil {
		return err, ""
	}
	var sensorDownloadAbsoluteUrl = path.Join(parsedEnterpriseServerUrl.Path, sensorDownloadRelativeUrl)
	var seekerTempFolder = filepath.Join(os.TempDir(), "seeker_tmp")
	os.RemoveAll(seekerTempFolder)
	err = os.MkdirAll(filepath.Dir(seekerTempFolder), 0755)
	if err != nil {
		return err, ""
	}
	sensorInstallerZipAbsolutePath := path.Join(seekerTempFolder, "SensorInstaller.zip")
	h.Log.Info("Downloading '%s' to '%s'", sensorDownloadAbsoluteUrl, sensorInstallerZipAbsolutePath)
	err = h.downloadFile(sensorDownloadAbsoluteUrl, sensorInstallerZipAbsolutePath)
	if err == nil {
		h.Log.Info("Download completed without errors")
	}
	if err != nil {
		return err, ""
	}
	// no native zip support for unzip - using shell utility
	err = h.Command.Execute("", os.Stdout, os.Stderr, "unzip "+sensorInstallerZipAbsolutePath, compiler.BuildDir())
	if err != nil {
		return err, ""
	}
	sensorJarFile := path.Join(seekerTempFolder,"SeekerInstaller.jar")
	agentPathInsideJarFile := "inline/agents/java/*"
	err = h.Command.Execute("", os.Stdout, os.Stderr, "unzip -j "+sensorJarFile +" " + agentPathInsideJarFile + " -d " + os.TempDir(), compiler.BuildDir())
	if err != nil {
		return err, ""
	}
	seekerLibraryPath := filepath.Join(os.TempDir(), "seeker-agent.tgz")
	if _, err := os.Stat(seekerLibraryPath); os.IsNotExist(err) {
		return errors.New("Could not find "+ seekerLibraryPath), ""
	}
	// Cleanup unneeded files
	os.Remove(seekerTempFolder)
	return err, seekerLibraryPath
}
func (h SeekerAfterCompileHook) updateNodeModules(pathToSeekerLibrary string, buildDir string) error {
	// No need to handle YARN, since NPM is installed even when YARN is the selected package manager
	if err := h.Command.Execute(buildDir, ioutil.Discard, ioutil.Discard, "npm", "install", "--save", pathToSeekerLibrary); err != nil {
		h.Log.Error("npm install --save " + pathToSeekerLibrary + " Error: " + err.Error())
		return err
	}
	return nil
}
func (h *SeekerAfterCompileHook) createSeekerEnvironmentScript(stager *libbuildpack.Stager) error {
	seekerEnvironmentScript := "seeker-env.sh"
	scriptContent := fmt.Sprintf("\nexport SEEKER_SENSOR_HOST=%s\nexport SEEKER_SENSOR_HTTP_PORT=%s", h.serviceCredentials.SensorHost, h.serviceCredentials.SensorPort)
	stager.Logger().Info(seekerEnvironmentScript + " content: " + scriptContent)
	return stager.WriteProfileD(seekerEnvironmentScript, scriptContent)
}

func extractServiceCredentials(Log *libbuildpack.Logger) (SeekerCredentials, error) {
	type UserProvidedService struct {
		BindingName interface{} `json:"binding_name"`
		Credentials struct {
			EnterpriseServerUrl string `json:"enterprise_server_url"`
			SensorHost string `json:"sensor_host"`
			SensorPort string `json:"sensor_port"`
		} `json:"credentials"`
		InstanceName   string   `json:"instance_name"`
		Label          string   `json:"label"`
		Name           string   `json:"name"`
		SyslogDrainURL string   `json:"syslog_drain_url"`
		Tags           []string `json:"tags"`
	}

	type VCAPSERVICES struct {
		UserProvidedService []UserProvidedService `json:"user-provided"`
	} //`json:"VCAP_SERVICES"`

	var vcapServices VCAPSERVICES
	err := json.Unmarshal([]byte(os.Getenv("VCAP_SERVICES")), &vcapServices)
	if err != nil {
		return SeekerCredentials{}, errors.New(fmt.Sprint("Failed to unmarshal VCAP_SERVICES: %s", err))
	}

	var detectedCredentials []UserProvidedService

	for _, service := range vcapServices.UserProvidedService {
		if isSeekerRelated(service.Name, service.Label) { // maybe add tags too
			detectedCredentials = append(detectedCredentials, service)
		}
	}
	if len(detectedCredentials) == 1 {
		Log.Info("Found one matching service: %s", detectedCredentials[0].Name)
		seekerCreds := SeekerCredentials{
			SensorHost:       detectedCredentials[0].Credentials.SensorHost,
			SensorPort:       detectedCredentials[0].Credentials.SensorPort,
			EnterpriseServerURL: detectedCredentials[0].Credentials.EnterpriseServerUrl}
		return seekerCreds, nil
	} else if len(detectedCredentials) > 1 {
		Log.Warning("More than one matching service found!")
	}

	return SeekerCredentials{}, nil
}
func isSeekerRelated(descriptors ... string) bool {
	isSeekerRelated := false
	for _, descriptor := range descriptors {
		containsSeeker, _ := regexp.MatchString(".*[sS][eE][eE][kK][eE][rR].*", descriptor)
		isSeekerRelated = isSeekerRelated || containsSeeker
	}
	return isSeekerRelated
}
func writeToFile(source io.Reader, destFile string, mode os.FileMode) error {
	err := os.MkdirAll(filepath.Dir(destFile), 0755)
	if err != nil {
		return err
	}

	fh, err := os.OpenFile(destFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer fh.Close()

	_, err = io.Copy(fh, source)
	if err != nil {
		return err
	}
	return nil
}
