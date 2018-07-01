package cmd

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/fatih/color"
	"github.com/pinpt/ripsrc/ripsrc"
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:  "ripsrc [dir,...]",
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		errors := make(chan error, 1)
		go func() {
			for err := range errors {
				fmt.Println(err)
				os.Exit(1)
			}
		}()
		var filter *ripsrc.Filter
		include, _ := cmd.Flags().GetString("include")
		exclude, _ := cmd.Flags().GetString("exclude")
		if include != "" || exclude != "" {
			filter = &ripsrc.Filter{}
			if include != "" {
				filter.Whitelist = regexp.MustCompile(include)
			}
			if exclude != "" {
				filter.Blacklist = regexp.MustCompile(exclude)
			}
		}
		var count int
		results := make(chan ripsrc.BlameResult, 10)
		resultsDone := make(chan bool, 1)
		go func() {
			for blame := range results {
				count++
				fmt.Printf("[%s] %s language=%s,loc=%v,sloc=%v,comments=%v,blanks=%v,complexity=%v\n", color.CyanString(blame.Commit.SHA[0:8]), color.GreenString(blame.Filename), color.MagentaString(blame.Language), blame.Loc, color.YellowString("%v", blame.Sloc), blame.Comments, blame.Comments, blame.Complexity)
			}
			resultsDone <- true
		}()
		started := time.Now()
		ripsrc.Rip(args, results, errors, filter)
		<-resultsDone
		fmt.Printf("finished processing %d commits from %d directories in %v\n", count, len(args), time.Since(started))
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	rootCmd.Flags().String("include", "", "include filter as a regular expression")
	rootCmd.Flags().String("exclude", "", "exclude filter as a regular expression")
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
