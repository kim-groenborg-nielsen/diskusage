package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
)

type Config struct {
	Root        string
	JsonOut     string
	ReadJson    string
	Environment string
	Profile     string
	Levels      int
	Concurrency int
	SizeWidth   int
	FilesWidth  int
	TopN        int
	ShowUser    bool
	ShowGroup   bool
	ShowFiles   bool
	BytesFlag   bool
	VersionFlag bool
}

var cachedConfig *Config

func GetConfig() *Config {
	if cachedConfig != nil {
		return cachedConfig
	}

	cachedConfig = &Config{
		Levels:      2,
		ShowUser:    false,
		ShowGroup:   false,
		ShowFiles:   false,
		Root:        ".",
		Concurrency: runtime.NumCPU() * 2,
		BytesFlag:   false,
		SizeWidth:   0,
		FilesWidth:  0,
		TopN:        0,
		JsonOut:     "",
		ReadJson:    "",
		VersionFlag: false,
		Environment: os.Getenv("ENVIRONMENT"),
		Profile:     os.Getenv("PROFILE"),
	}

	flag.IntVar(&cachedConfig.Levels, "levels", cachedConfig.Levels, "number of directory levels to display (0 means only root)")
	flag.BoolVar(&cachedConfig.ShowUser, "user", cachedConfig.ShowUser, "show directory owner user")
	flag.BoolVar(&cachedConfig.ShowGroup, "group", cachedConfig.ShowGroup, "show directory owner group")
	flag.BoolVar(&cachedConfig.ShowFiles, "files", cachedConfig.ShowFiles, "show number of files per directory")
	flag.StringVar(&cachedConfig.Root, "root", cachedConfig.Root, "root path to analyze (can also be specified as the first positional argument)")
	flag.IntVar(&cachedConfig.Concurrency, "concurrency", cachedConfig.Concurrency, "number of concurrent directory readers")
	flag.BoolVar(&cachedConfig.BytesFlag, "bytes", cachedConfig.BytesFlag, "print sizes in bytes instead of human-readable format")
	flag.IntVar(&cachedConfig.SizeWidth, "size-width", cachedConfig.SizeWidth, "override size column width (0 = auto-fit)")
	flag.IntVar(&cachedConfig.FilesWidth, "files-width", cachedConfig.FilesWidth, "override files column width (0 = auto-fit)")
	flag.IntVar(&cachedConfig.TopN, "top", cachedConfig.TopN, "limit per-user/group list to top N entries (0 = no limit)")
	flag.StringVar(&cachedConfig.JsonOut, "json", cachedConfig.JsonOut, "write JSON summary to file or '-' for stdout")
	flag.StringVar(&cachedConfig.ReadJson, "read-json", cachedConfig.ReadJson, "read JSON summary from file and print it (no directory scanning)")
	flag.BoolFunc("version", "show version and exit", func(string) error {
		println("Version: ", version)
		println("Commit:  ", commit)
		println("Date:    ", date)
		os.Exit(0)
		return nil
	})

	flag.Usage = func() {
		w := flag.CommandLine.Output()
		_, _ = fmt.Fprintf(w, "Usage: %s [options] <root>\n\n", os.Args[0])
		_, _ = fmt.Fprintln(w, "Options:")
		flag.PrintDefaults()
		_, _ = fmt.Fprintln(w, "\nNote: options must be specified before the positional <root> argument.")
		_, _ = fmt.Fprintf(w, "Example: %s -levels 3 -user -group -bytes /path/to/analyze\n", os.Args[0])
	}

	flag.Parse()

	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			flag.Usage()
			os.Exit(0)
		}
	}

	return cachedConfig
}
