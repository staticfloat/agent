package clicommand

import (
	"io/ioutil"
	"os"
	"time"

	"github.com/buildkite/agent/v3/stdin"

	"github.com/buildkite/agent/v3/api"
	"github.com/buildkite/agent/v3/cliconfig"
	"github.com/buildkite/agent/v3/retry"
	"github.com/urfave/cli"
)

var AnnotateHelpDescription = `Usage:

   buildkite-agent annotate [<body>] [arguments...]

Description:

   Build annotations allow you to customize the Buildkite build interface to
   show information that may surface from your builds. Some examples include:

   - Links to artifacts generated by your jobs
   - Test result summaries
   - Graphs that include analysis about your codebase
   - Helpful information for team members about what happened during a build

   Annotations are written in CommonMark-compliant Markdown, with "GitHub
   Flavored Markdown" extensions.

   The annotation body can be supplied as a command line argument, or by piping
   content into the command.

   You can update an existing annotation's body by running the annotate command
   again and provide the same context as the one you want to update. Or if you
   leave context blank, it will use the default context.

   You can also update just the style of an existing annotation by omitting the
   body entirely and providing a new style value.

Example:

   $ buildkite-agent annotate "All tests passed! :rocket:"
   $ cat annotation.md | buildkite-agent annotate --style "warning"
   $ buildkite-agent annotate --style "success" --context "junit"
   $ ./script/dynamic_annotation_generator | buildkite-agent annotate --style "success"`

type AnnotateConfig struct {
	Body    string `cli:"arg:0" label:"annotation body"`
	Style   string `cli:"style"`
	Context string `cli:"context"`
	Append  bool   `cli:"append"`
	Job     string `cli:"job" validate:"required"`

	// Global flags
	Debug   bool         `cli:"debug"`
	NoColor bool         `cli:"no-color"`
	Experiments []string `cli:"experiment" normalize:"list"`
	Profile string       `cli:"profile"`

	// API config
	DebugHTTP        bool   `cli:"debug-http"`
	AgentAccessToken string `cli:"agent-access-token" validate:"required"`
	Endpoint         string `cli:"endpoint" validate:"required"`
	NoHTTP2          bool   `cli:"no-http2"`
}

var AnnotateCommand = cli.Command{
	Name:        "annotate",
	Usage:       "Annotate the build page within the Buildkite UI with text from within a Buildkite job",
	Description: AnnotateHelpDescription,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:   "context",
			Usage:  "The context of the annotation used to differentiate this annotation from others",
			EnvVar: "BUILDKITE_ANNOTATION_CONTEXT",
		},
		cli.StringFlag{
			Name:   "style",
			Usage:  "The style of the annotation (`success`, `info`, `warning` or `error`)",
			EnvVar: "BUILDKITE_ANNOTATION_STYLE",
		},
		cli.BoolFlag{
			Name:   "append",
			Usage:  "Append to the body of an existing annotation",
			EnvVar: "BUILDKITE_ANNOTATION_APPEND",
		},
		cli.StringFlag{
			Name:   "job",
			Value:  "",
			Usage:  "Which job should the annotation come from",
			EnvVar: "BUILDKITE_JOB_ID",
		},

		// API Flags
		AgentAccessTokenFlag,
		EndpointFlag,
		NoHTTP2Flag,
		DebugHTTPFlag,

		// Global flags
		NoColorFlag,
		DebugFlag,
		ExperimentsFlag,
		ProfileFlag,
	},
	Action: func(c *cli.Context) {
		// The configuration will be loaded into this struct
		cfg := AnnotateConfig{}

		l := CreateLogger(&cfg)

		// Load the configuration
		if err := cliconfig.Load(c, l, &cfg); err != nil {
			l.Fatal("%s", err)
		}

		// Setup any global configuration options
		done := HandleGlobalFlags(l, cfg)
		defer done()

		var body string
		var err error

		if cfg.Body != "" {
			body = cfg.Body
		} else if stdin.IsReadable() {
			l.Info("Reading annotation body from STDIN")

			// Actually read the file from STDIN
			stdin, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				l.Fatal("Failed to read from STDIN: %s", err)
			}

			body = string(stdin[:])
		}

		// Create the API client
		client := api.NewClient(l, loadAPIClientConfig(cfg, `AgentAccessToken`))

		// Create the annotation we'll send to the Buildkite API
		annotation := &api.Annotation{
			Body:    body,
			Style:   cfg.Style,
			Context: cfg.Context,
			Append:  cfg.Append,
		}

		// Retry the annotation a few times before giving up
		err = retry.Do(func(s *retry.Stats) error {
			// Attempt to create the annotation
			resp, err := client.Annotate(cfg.Job, annotation)

			// Don't bother retrying if the response was one of these statuses
			if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 404 || resp.StatusCode == 400) {
				s.Break()
				return err
			}

			// Show the unexpected error
			if err != nil {
				l.Warn("%s (%s)", err, s)
			}

			return err
		}, &retry.Config{Maximum: 5, Interval: 1 * time.Second, Jitter: true})

		// Show a fatal error if we gave up trying to create the annotation
		if err != nil {
			l.Fatal("Failed to annotate build: %s", err)
		}

		l.Debug("Successfully annotated build")
	},
}
