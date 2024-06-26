package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gokyle/goconfig"
	"suah.dev/protect"
)

// / Configuration Globals
var (
	c                              goconfig.ConfigMap
	temp_image_files_dir_          string
	feed2matrx_image_bytes_limit_  int64
	feed2matrx_image_count_limit_  int
	matrix_notice_character_limit_ int = 1000
	matrix_image_timeout_          time.Duration
	poststuffreminder_timeout_     time.Duration
	poststuffreminder_msg_         string
)

type ConfigValueDescriptor struct {
	ConfSection string
	ConfName    string
	Default     string
}

/// Function Name Coding Standard
/// func runMyFunction    ... function that does not return and could be run a gorouting, e.g. go runMyFunction
/// func taskMyFunction  ... function that internally lauches a goroutine

func hasStringMatchingPrefix(a, b string) bool {
	minlen := len(a)
	if len(b) < minlen {
		minlen = len(b)
	}
	return a[:minlen] == b[:minlen]

}

func configSanityChecksAndDefaults() {
	must_be_unique_and_present_configvalues := []ConfigValueDescriptor{
		ConfigValueDescriptor{"matrix", "guard_prefix", "t>"},
		ConfigValueDescriptor{"matrix", "directtoot_prefix", "private_dm>"},
		ConfigValueDescriptor{"matrix", "tootreply_prefix", "public_reply2>"},
		ConfigValueDescriptor{"matrix", "directtweet_prefix", "tdm>"},
		ConfigValueDescriptor{"matrix", "reblog_prefix", "reblog>"},
		ConfigValueDescriptor{"matrix", "favourite_prefix", "+1>"},
		ConfigValueDescriptor{"matrix", "help_prefix", "!help"},
		ConfigValueDescriptor{"matrix", "mediadesc_prefix", "desc>"},
	}

	for _, cfgval := range must_be_unique_and_present_configvalues {
		if !c.SectionInConfig(cfgval.ConfSection) {
			panic(fmt.Sprintf("ERROR: config section [%s] must exist!", cfgval.ConfSection))
		}
		cmd := strings.TrimSpace(c.GetValueDefault(cfgval.ConfSection, cfgval.ConfName, cfgval.Default))
		if strings.ContainsAny(cmd, "\t \n") {
			panic(fmt.Sprintf("ERROR: config value [%s]%s cannot contain whitespace!", cfgval.ConfSection, cfgval.ConfName))
		}
		if len(cmd) == 0 {
			panic(fmt.Sprintf("ERROR: config value [%s]%s cannot be empty!", cfgval.ConfSection, cfgval.ConfName))
		}
		c[cfgval.ConfSection][cfgval.ConfName] = cmd
	}
	for idx1, cfgval1 := range must_be_unique_and_present_configvalues[0 : len(must_be_unique_and_present_configvalues)-1] {
		for _, cfgval2 := range must_be_unique_and_present_configvalues[idx1+1:] {
			cmd1 := c[cfgval1.ConfSection][cfgval1.ConfName]
			cmd2 := c[cfgval2.ConfSection][cfgval2.ConfName]
			if hasStringMatchingPrefix(cmd1, cmd2) {
				panic(fmt.Sprintf("ERROR: [%s]%s and [%s]%s MUST differ and not overlap", cfgval1.ConfSection, cfgval1.ConfName, cfgval2.ConfSection, cfgval2.ConfName))
			}
		}
	}
}

func mainWithDefers() {
	var err error
	//// Create image temp dir if needed
	if c.GetValueDefault("images", "enabled", "false") == "true" {
		temp_image_files_dir_, err = ioutil.TempDir(c.GetValueDefault("images", "temp_dir", "/tmp"), "mycete")
		if err != nil {
			panic(err)
		}
		if err = os.Chmod(temp_image_files_dir_, 0700); err != nil {
			panic(err)
		}
		defer os.RemoveAll(temp_image_files_dir_)
	}

	///////////////////////////////////////////////////////////
	//// Start Bot and all Sub-Go-Routines
	go runMatrixPublishBot()

	///////////////////////////////////////////////////////////
	//// wait until Signal, then quit
	{
		ctrlc_c := make(chan os.Signal, 1)
		signal.Notify(ctrlc_c, os.Interrupt, os.Kill, syscall.SIGTERM)
		<-ctrlc_c //block until ctrl+c is pressed || we receive SIGINT aka kill -1 || kill
	}
}

func main() {
	var err error

	cfile := flag.String("conf", "/etc/mycete.conf", "Configuration file")
	flag.Parse()

	_ = protect.Pledge("stdio rpath cpath wpath fattr inet dns")

	c, err = goconfig.ParseFile(*cfile)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	///////////////////////////////////////////////////////////
	//// pre-read and initialize gloabl configuration variables

	if c_charlimitstr, c_charlimitstr_set := c.GetValue("feed2matrix", "characterlimit"); c_charlimitstr_set && len(c_charlimitstr) > 0 {
		if charlimit, err := strconv.Atoi(c_charlimitstr); err == nil {
			matrix_notice_character_limit_ = charlimit
		}
	}

	if feed2matrx_image_bytes_limit_, err = strconv.ParseInt(c.GetValueDefault("feed2matrix", "imagebyteslimit", "4194304"), 10, 64); err != nil {
		panic(err)
	}
	if feed2matrx_image_count_limit_, err = strconv.Atoi(c.GetValueDefault("feed2matrix", "imagecountlimit", "4")); err != nil {
		panic(err)
	}
	if matrix_image_timeout_mins, err := strconv.Atoi(c.GetValueDefault("matrix", "image_timeout_minutes", "120")); err != nil {
		if matrix_image_timeout_mins, err = strconv.Atoi(c.GetValueDefault("feed2matrix", "image_timeout_minutes", "120")); err != nil {
			panic(err)
		} else {
			matrix_image_timeout_ = time.Minute * time.Duration(matrix_image_timeout_mins)
		}
	} else {
		matrix_image_timeout_ = time.Minute * time.Duration(matrix_image_timeout_mins)
	}
	if feed2matrx_image_timeout_d, err := time.ParseDuration(c.GetValueDefault("matrix", "image_timeout_duration", "")); err == nil {
		matrix_image_timeout_ = feed2matrx_image_timeout_d
	}

	configSanityChecksAndDefaults()

	if poststuffreminder_timeout_str_ := c.GetValueDefault("matrix", "poststuffreminder_timeout", ""); len(poststuffreminder_timeout_str_) > 0 {
		if poststuffreminder_timeout_, err = time.ParseDuration(poststuffreminder_timeout_str_); err != nil {
			panic("ERROR: Could not parse duration from config value 'poststuffreminder_timeout'. Leave blank to disable.")
		} else {
			poststuffreminder_msg_ = c.GetValueDefault("matrix", "poststuffreminder_msg", "Hey, you wanted to be reminded to post something!")
		}
	}

	////////////////////////////////////////////////////////////
	//// run main Main where a defer will still be called before we exit
	mainWithDefers()
	fmt.Println("Exiting")
}
