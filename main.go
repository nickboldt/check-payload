package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/pprof"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/openshift/check-payload/dist/releases"
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"

	"github.com/openshift/check-payload/internal/scan"
	"github.com/openshift/check-payload/internal/types"
)

const (
	defaultPayloadFilename = "payload.json"
	defaultConfigFile      = "config.toml"
)

//go:embed config.toml
var embeddedConfig string

var applicationDeps = []string{
	"file",
	"go",
	"nm",
	"oc",
	"podman",
}

var applicationDepsNodeScan = []string{
	"file",
	"go",
	"nm",
	"rpm",
}

var Commit string

var (
	components                            []string
	configFile, configForVersion          string
	cpuProfile                            string
	failOnWarnings                        bool
	filterFiles, filterDirs, filterImages []string
	insecurePull                          bool
	limit                                 int
	outputFile                            string
	outputFormat                          string
	parallelism                           int
	printExceptions                       bool
	pullSecretFile                        string
	timeLimit                             time.Duration
	verbose                               bool
)

func main() {
	var config types.Config
	var results []*types.ScanResults

	rootCmd := cobra.Command{
		Use:           "check-payload",
		SilenceErrors: true,
	}
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "verbose")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(Commit)
			return nil
		},
	}

	scanCmd := &cobra.Command{
		Use:   "scan",
		Short: "Run a scan",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := getConfig(&config); err != nil {
				return err
			}
			config.FailOnWarnings = failOnWarnings
			config.FilterFiles = append(config.FilterFiles, filterFiles...)
			config.FilterDirs = append(config.FilterDirs, filterDirs...)
			config.FilterImages = append(config.FilterImages, filterImages...)
			config.Parallelism = parallelism
			config.InsecurePull = insecurePull
			config.OutputFile = outputFile
			config.OutputFormat = outputFormat
			config.PrintExceptions = printExceptions
			config.PullSecret = pullSecretFile
			config.Limit = limit
			config.TimeLimit = timeLimit
			config.Verbose = verbose
			config.Log()
			klog.InfoS("scan", "version", Commit)

			if cpuProfile != "" {
				f, err := os.Create(cpuProfile)
				if err != nil {
					return err
				}
				if err := pprof.StartCPUProfile(f); err != nil {
					return err
				}
				klog.Info("collecting CPU profile data to ", cpuProfile)
			}

			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			if cpuProfile != "" {
				pprof.StopCPUProfile()
				klog.Info("CPU profile saved to ", cpuProfile)
			}
			scan.PrintResults(&config, results)
			if scan.IsFailed(results) {
				return errors.New("run failed")
			}
			if scan.IsWarnings(results) && config.FailOnWarnings {
				return errors.New("run failed with warnings")
			}
			return nil
		},
	}
	scanCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "use toml config file (default: "+defaultConfigFile+")")
	scanCmd.PersistentFlags().StringVarP(&configForVersion, "config-for-version", "V", "", "use embedded toml config file for specified version")
	scanCmd.PersistentFlags().StringSliceVar(&filterFiles, "filter-files", nil, "")
	scanCmd.PersistentFlags().StringSliceVar(&filterDirs, "filter-dirs", nil, "")
	scanCmd.PersistentFlags().StringSliceVar(&filterImages, "filter-images", nil, "")
	scanCmd.PersistentFlags().StringSliceVar(&components, "components", nil, "")
	scanCmd.PersistentFlags().BoolVar(&failOnWarnings, "fail-on-warnings", false, "fail on warnings")
	scanCmd.PersistentFlags().BoolVar(&insecurePull, "insecure-pull", false, "use insecure pull")
	scanCmd.PersistentFlags().IntVar(&limit, "limit", -1, "limit the number of pods scanned")
	scanCmd.PersistentFlags().IntVar(&parallelism, "parallelism", 5, "how many pods to check at once")
	scanCmd.PersistentFlags().StringVar(&outputFile, "output-file", "", "write report to file")
	scanCmd.PersistentFlags().StringVar(&outputFormat, "output-format", "table", "output format (table, csv, markdown, html)")
	scanCmd.PersistentFlags().StringVar(&pullSecretFile, "pull-secret", "", "pull secret to use for pulling images")
	scanCmd.PersistentFlags().DurationVar(&timeLimit, "time-limit", 1*time.Hour, "limit running time")
	scanCmd.PersistentFlags().StringVar(&cpuProfile, "cpuprofile", "", "write CPU profile to file")
	scanCmd.PersistentFlags().BoolVarP(&printExceptions, "print-exceptions", "p", false, "display exception list")

	scanPayload := &cobra.Command{
		Use:          "payload [image pull spec]",
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return scan.ValidateApplicationDependencies(applicationDeps)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeLimit)
			defer cancel()
			config.FromURL, _ = cmd.Flags().GetString("url")
			config.FromFile, _ = cmd.Flags().GetString("file")
			config.PrintExceptions, _ = cmd.Flags().GetBool("print-exceptions")
			results = scan.RunPayloadScan(ctx, &config)
			return nil
		},
	}
	scanPayload.Flags().StringP("url", "u", "", "payload url")
	scanPayload.Flags().StringP("file", "f", "", "payload from json file")
	scanPayload.MarkFlagsMutuallyExclusive("url", "file")

	scanNode := &cobra.Command{
		Use:          "node [--root /myroot]",
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return scan.ValidateApplicationDependencies(applicationDepsNodeScan)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeLimit)
			defer cancel()
			config.NodeScan, _ = cmd.Flags().GetString("root")
			results = scan.RunNodeScan(ctx, &config)
			return nil
		},
	}
	scanNode.Flags().String("root", "", "root path to scan")
	_ = scanNode.MarkFlagRequired("root")

	scanImage := &cobra.Command{
		Use:          "image [image pull spec]",
		Aliases:      []string{"operator"},
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return scan.ValidateApplicationDependencies(applicationDeps)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), timeLimit)
			defer cancel()
			config.ContainerImage, _ = cmd.Flags().GetString("spec")
			results = scan.RunOperatorScan(ctx, &config)
			return nil
		},
	}
	scanImage.Flags().String("spec", "", "payload url")
	_ = scanImage.MarkFlagRequired("spec")

	scanCmd.AddCommand(scanPayload)
	scanCmd.AddCommand(scanNode)
	scanCmd.AddCommand(scanImage)

	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(scanCmd)

	// Add klog flags.
	klogFlags := flag.NewFlagSet("", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	rootCmd.PersistentFlags().AddGoFlagSet(klogFlags)

	if err := rootCmd.Execute(); err != nil {
		klog.Fatalf("Error: %v\n", err)
	}
}

func getConfig(config *types.Config) (retErr error) {
	var (
		res toml.MetaData
		err error
	)

	// Check if the configuration was fully parsed.
	defer func() {
		if retErr != nil {
			return
		}
		un := res.Undecoded()
		if len(un) != 0 {
			retErr = fmt.Errorf("unknown keys in config: %+v", un)
		}
	}()

	// Handle --config-for-version first.
	if configForVersion != "" {
		if configFile != "" {
			return errors.New("can't use both --config and --config-for-version")
		}
		cfg, err := releases.GetConfigFor(configForVersion)
		if err != nil {
			return err
		}
		res, err = toml.Decode(string(cfg), &config)
		if err != nil { // Should never happen.
			panic("invalid embedded config: " + err.Error())
		}
		return nil
	}

	// Handle --config.
	file := configFile
	if file == "" {
		file = defaultConfigFile
	}
	res, err = toml.DecodeFile(file, &config)
	if err == nil {
		klog.Infof("using config file: %v", file)
		return nil
	}

	// When neither --config not --config-for-version are specified, and
	// defaultConfigFile is not found, fall back to embedded config.
	if errors.Is(err, os.ErrNotExist) && configFile == "" {
		klog.Info("using embedded config")
		res, err = toml.Decode(embeddedConfig, &config)
		if err != nil { // Should never happen.
			panic("invalid embedded config: " + err.Error())
		}
		return nil
	}
	// Otherwise, error out.
	return fmt.Errorf("can't parse config file %q: %w", file, err)
}
