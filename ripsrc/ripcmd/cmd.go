package ripcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/pkg/profile"

	"github.com/fatih/color"
	"github.com/pinpt/ripsrc/ripsrc"
)

type Opts struct {
	// Dir is directory to run ripsrc on.
	// If it contains .git directory inside, this dir will be processed.
	// If the dir name ends with .git and has objects dir inside it will be assumed to be bare repo and processed.
	// If neither of this is true if will process containing dirs following the same algo.
	Dir string

	// CommitFromIncl starts from specific commit (inclusive). May also include some previous commits.
	CommitFromIncl string

	// Profile set to one of mem, mutex, cpu, block, trace to enable profiling.
	Profile string
}

type Stats struct {
	Repos             int
	SkippedEmptyRepos int
	Entries           int
}

type RepoError struct {
	Repo string
	Err  error
}

func Run(ctx context.Context, out io.Writer, opts Opts) {
	start := time.Now()

	if opts.Profile != "" {
		runEndHook := enableProfiling(opts.Profile)
		defer runEndHook()
	}

	stats, repoErrs, err := runOnDirs(ctx, out, opts, opts.Dir, 1, start)
	if err != nil {
		fmt.Println("failed processing with err", err)
		os.Exit(1)
	}
	if len(repoErrs) != 0 {
		fmt.Fprintln(color.Output, color.RedString("failed processing, errors:"))
		for _, err := range repoErrs {
			fmt.Fprintln(color.Output, color.RedString("repo: %v err: %v\n", err.Repo, err.Err))
		}
		fmt.Fprintln(color.Output, color.RedString("failed processing"))
		os.Exit(1)
	}
	if stats.Repos == 0 {
		fmt.Fprintln(color.Output, color.RedString("failed processing, no git repos found in supplied dir: %v", opts.Dir))
		os.Exit(1)
	}
	if stats.SkippedEmptyRepos != 0 {
		fmt.Fprintf(color.Output, "%v", color.YellowString("Warning! Skipped %v empty repos\n", stats.SkippedEmptyRepos))
	}
	fmt.Fprintf(color.Output, "%v", color.GreenString("Finished processing repos %d entries %d in %v\n", stats.Repos, stats.Entries, time.Since(start)))
}

func enableProfiling(kind string) (onEnd func()) {
	dir, _ := ioutil.TempDir("", "ripsrc-profile")

	var stop func()

	onEnd = func() {
		stop()
		fn := filepath.Join(dir, kind+".pprof")
		fmt.Printf("to view profile, run `go tool pprof --pdf %s`\n", fn)
	}

	switch kind {
	case "cpu":
		{
			stop = profile.Start(profile.CPUProfile, profile.ProfilePath(dir), profile.Quiet).Stop
		}
	case "mem":
		{
			stop = profile.Start(profile.MemProfile, profile.ProfilePath(dir), profile.Quiet).Stop
		}
	case "trace":
		{
			stop = profile.Start(profile.TraceProfile, profile.ProfilePath(dir), profile.Quiet).Stop
		}
	case "block":
		{
			stop = profile.Start(profile.BlockProfile, profile.ProfilePath(dir), profile.Quiet).Stop
		}
	case "mutex":
		{
			stop = profile.Start(profile.MutexProfile, profile.ProfilePath(dir), profile.Quiet).Stop
		}
	default:
		{
			panic("unexpected profile: " + kind)
		}
	}
	return
}

func runOnDirs(ctx context.Context, wr io.Writer, opts Opts, dir string, recurseLevels int, start time.Time) (stats Stats, repoErrors []RepoError, rerr error) {
	stat, err := os.Stat(dir)
	if err != nil {
		rerr = fmt.Errorf("can't stat passed dir, err: %v", err)
		return
	}
	if !stat.IsDir() {
		rerr = fmt.Errorf("passed dir is a file, expecting a dir")
		return
	}
	// check if contains .git
	containsDotGit, err := dirContainsDir(dir, ".git")
	if err != nil {
		rerr = err
		return
	}
	run := func() {
		entries, err := runOnRepo(ctx, wr, opts, dir, start)
		if err != nil && err != errRevParseFailed {
			repoErrors = []RepoError{{Repo: dir, Err: err}}
			return
		}
		stats.Repos = 1
		stats.Entries = entries
		if err == errRevParseFailed {
			stats.SkippedEmptyRepos++
		}
	}

	if containsDotGit {
		run()
		return
	}

	loc, err := filepath.Abs(dir)
	if err != nil {
		rerr = fmt.Errorf("can't convert passed dir to absolute path, err: %v", err)
		return
	}

	if filepath.Ext(loc) == ".git" {
		containsObjects, err := dirContainsDir(dir, "objects")
		if err != nil {
			rerr = err
			return
		}
		if containsObjects {
			run()
			return
		}
	}

	if recurseLevels == 0 {
		return
	}

	subs, err := ioutil.ReadDir(dir)
	if err != nil {
		rerr = fmt.Errorf("can't read passed dir, err: %v", err)
		return
	}

	for _, sub := range subs {
		if !sub.IsDir() {
			continue
		}
		subEntries, subErrs, err := runOnDirs(ctx, wr, opts, filepath.Join(dir, sub.Name()), recurseLevels-1, start)
		repoErrors = append(repoErrors, subErrs...)
		stats.Repos += subEntries.Repos
		stats.Entries += subEntries.Entries
		stats.SkippedEmptyRepos += subEntries.SkippedEmptyRepos
		if err != nil {
			rerr = err
			return
		}
	}
	return
}

func dirContainsDir(dir string, sub string) (bool, error) {
	stat, err := os.Stat(filepath.Join(dir, sub))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		} else {
			return false, fmt.Errorf("can't check if dir contains %v, dir: %v err: %v", sub, dir, err)
		}
	}
	if !stat.IsDir() {
		return false, nil
	}
	return true, nil
}

var errRevParseFailed = errors.New("git rev-parse HEAD failed")

func runOnRepo(ctx context.Context, wr io.Writer, opts Opts, repoDir string, globalStart time.Time) (entries int, _ error) {
	start := time.Now()
	fmt.Fprintf(color.Output, "starting processing repo:%v\n", color.GreenString(repoDir))
	if !hasHeadCommit(ctx, repoDir) {
		fmt.Fprintf(wr, "git rev-parse HEAD failed, happens for empty repos, repo: %v \n", repoDir)
		return 0, errRevParseFailed
	}

	ripper := ripsrc.New()

	res := make(chan ripsrc.BlameResult)
	done := make(chan bool)
	go func() {
		allocatedMem := getAllocatedMemMB()
		allocatedMemMu := &sync.Mutex{}
		ticker := time.NewTicker(time.Second)

		go func() {
			for {
				<-ticker.C
				allocatedMemMu.Lock()
				allocatedMem = getAllocatedMemMB()
				allocatedMemMu.Unlock()

				timeSinceStartMin := int(time.Since(globalStart).Minutes())
				fmt.Fprintf(color.Output, "[%sm][%vMB]\n", color.YellowString("%v", timeSinceStartMin), color.YellowString("%v", allocatedMem))
			}
		}()

		for blame := range res {
			entries++
			var license string
			if blame.License != nil {
				license = fmt.Sprintf("%v (%.0f%%)", color.RedString(blame.License.Name), 100*blame.License.Confidence)
			}
			timeSinceStartMin := int(time.Since(globalStart).Minutes())
			allocatedMemMu.Lock()
			mem := allocatedMem
			allocatedMemMu.Unlock()
			fmt.Fprintf(color.Output, "[%s][%s][%sm][%vMB] %s language=%s,license=%v,loc=%v,sloc=%v,comments=%v,blanks=%v,complexity=%v,skipped=%v,status=%s,author=%s\n", color.YellowString("%v", repoDir), color.CyanString(blame.Commit.SHA[0:8]), color.YellowString("%v", timeSinceStartMin), color.YellowString("%v", mem), color.GreenString(blame.Filename), color.MagentaString(blame.Language), license, blame.Loc, color.YellowString("%v", blame.Sloc), blame.Comments, blame.Comments, blame.Complexity, blame.Skipped, blame.Commit.Files[blame.Filename].Status, blame.Commit.Author())

		}
		done <- true
	}()

	ripOpts := &ripsrc.RipOpts{}
	ripOpts.CommitFromIncl = opts.CommitFromIncl
	ripOpts.NoStrictResume = true
	err := ripper.Rip(ctx, repoDir, res, ripOpts)
	<-done

	if err != nil {
		return entries, err
	}

	fmt.Fprintln(wr)
	ripper.GitProcessTimings.OutputStats(wr)
	fmt.Fprintln(wr)
	ripper.CodeInfoTimings.OutputStats(wr)
	fmt.Fprintln(wr)

	fmt.Fprintf(color.Output, "finished repo processing for %v in %v. %d entries processed\n", color.HiGreenString(repoDir), time.Since(start), entries)

	return entries, nil
}

func getAllocatedMemMB() int {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int(m.HeapAlloc / 1024 / 1024)
}

func hasHeadCommit(ctx context.Context, repoDir string) bool {
	out := bytes.NewBuffer(nil)
	c := exec.Command("git", "rev-parse", "HEAD")
	c.Dir = repoDir
	c.Stdout = out
	c.Run()
	res := strings.TrimSpace(out.String())
	if len(res) != 40 {
		return false
	}
	return true
}
