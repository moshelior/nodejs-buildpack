package hooks_test

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"bytes"
	"github.com/cloudfoundry/libbuildpack"
	"github.com/golang/mock/gomock"
	"nodejs/hooks"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"gopkg.in/jarcoal/httpmock.v1"
)

var _ = Describe("seekerHook", func() {
	var (
		err          error
		buildDir     string
		depsDir      string
		depsIdx      string
		logger       *libbuildpack.Logger
		stager       *libbuildpack.Stager
		mockCtrl     *gomock.Controller
		mockCommand  *MockCommand
		buffer       *bytes.Buffer
		seeker       hooks.SeekerAfterCompileHook
	)

	BeforeEach(func() {
		buildDir, err = ioutil.TempDir("", "nodejs-buildpack.build.")
		Expect(err).To(BeNil())

		depsDir, err = ioutil.TempDir("", "nodejs-buildpack.deps.")
		Expect(err).To(BeNil())

		depsIdx = "07"
		err = os.MkdirAll(filepath.Join(depsDir, depsIdx), 0755)

		buffer = new(bytes.Buffer)
		logger = libbuildpack.NewLogger(buffer)

		mockCommand = NewMockCommand(mockCtrl)
		logger := libbuildpack.NewLogger(os.Stdout)
		command := &libbuildpack.Command{}

		seeker = hooks.SeekerAfterCompileHook{
			Command: command,
			Log:     logger,
		}

	})

	JustBeforeEach(func() {
		args := []string{buildDir, "", depsDir, depsIdx}
		stager = libbuildpack.NewStager(args, logger, &libbuildpack.Manifest{})
	})

	AfterEach(func() {

		err = os.RemoveAll(buildDir)
		Expect(err).To(BeNil())

		err = os.RemoveAll(depsDir)
		Expect(err).To(BeNil())
	})

	Describe("AfterCompile", func() {
		var (
			oldVcapApplication string
			oldVcapServices    string
			oldBpDebug         string
		)
		BeforeEach(func() {
			oldVcapApplication = os.Getenv("VCAP_APPLICATION")
			oldVcapServices = os.Getenv("VCAP_SERVICES")
			oldBpDebug = os.Getenv("BP_DEBUG")
			httpmock.Deactivate()

		})
		AfterEach(func() {
			os.Setenv("VCAP_APPLICATION", oldVcapApplication)
			os.Setenv("VCAP_SERVICES", oldVcapServices)
			os.Setenv("BP_DEBUG", oldBpDebug)
			httpmock.Activate()
		})

		Context("VCAP_SERVICES contains seeker service - as a user provided service", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"pcf app"}`)
				os.Setenv("VCAP_SERVICES", `{
    "user-provided": [
      {
        "name": "seeker_service_v2",
        "instance_name": "seeker_service_v2",
        "binding_name": null,
        "credentials": {
          "enterprise_server_url": "http://10.120.8.113:8082",
          "sensor_host": "localhost",
          "sensor_port": "9911"
        },
        "syslog_drain_url": "",
        "volume_mounts": [],
        "label": "user-provided",
        "tags": []
      }
    ]
  }`)

			})
			It("installs seeker", func() {
				err = seeker.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := ioutil.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", "seeker-env.sh"))
				Expect(err).To(BeNil())

				Expect(string(contents)).To(Equal("\n" +
					"export SEEKER_SENSOR_HOST=localhost\n" +
					"export SEEKER_SENSOR_HTTP_PORT=9911"))
			})
		})
		Context("VCAP_SERVICES contains seeker service - as a regular service", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", `{"name":"pcf app"}`)
				os.Setenv("VCAP_SERVICES", `{
  "seeker-security-service": [
    {
      "name": "seeker_instace",
      "instance_name": "seeker_instace",
      "binding_name": null,
      "credentials": {
        "sensor_host": "localhost",
        "sensor_port": "9911",
        "enterprise_server_url": "http://10.120.8.113:8082"
      },
      "syslog_drain_url": null,
      "volume_mounts": [],
      "label": "seeker-security-service",
      "provider": null,
      "plan": "default-seeker-plan-new",
      "tags": [
        "security",
        "agent",
        "monitoring"
      ]
    }
  ],
"2": [{"name":"mysql"}]}
`)

			})
			It("installs seeker", func() {
				err = seeker.AfterCompile(stager)
				Expect(err).To(BeNil())

				// Sets up profile.d
				contents, err := ioutil.ReadFile(filepath.Join(depsDir, depsIdx, "profile.d", "seeker-env.sh"))
				Expect(err).To(BeNil())

				Expect(string(contents)).To(Equal("\n" +
					"export SEEKER_SENSOR_HOST=localhost\n" +
					"export SEEKER_SENSOR_HTTP_PORT=9911"))
			})
		})
	})
})
