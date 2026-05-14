package commands

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/git-lfs/git-lfs/v3/filepathfilter"
	"github.com/git-lfs/git-lfs/v3/git"
	"github.com/git-lfs/git-lfs/v3/lfs"
	"github.com/git-lfs/git-lfs/v3/lfshttp"
	"github.com/git-lfs/git-lfs/v3/tasklog"
	"github.com/git-lfs/git-lfs/v3/tq"
	"github.com/git-lfs/git-lfs/v3/tr"
	"github.com/rubyist/tracerx"
	"github.com/spf13/cobra"
)

var pullDangerouslyHardlinkToWorktree bool

func pullCommand(cmd *cobra.Command, args []string) {
	requireGitVersion()
	setupRepository()

	if len(args) > 0 {
		// Remote is first arg
		if err := cfg.SetValidRemote(args[0]); err != nil {
			Exit(tr.Tr.Get("Invalid remote name %q: %s", args[0], err))
		}
	}

	includeArg, excludeArg := getIncludeExcludeArgs(cmd)
	filter := buildFilepathFilter(cfg, includeArg, excludeArg, true)
	if pullDangerouslyHardlinkToWorktree {
		fmt.Fprintln(os.Stderr, tr.Tr.Get("WARNING: --dangerously-hardlink-to-worktree is enabled. Worktree files will share inodes with the LFS object cache; modifying any worktree file will corrupt the cached object. Use only in ephemeral environments."))
	}
	pull(filter)
}

func pull(filter *filepathfilter.Filter) {
	ref, err := git.CurrentRef()
	if err != nil {
		Panic(err, tr.Tr.Get("Could not pull"))
	}

	pointers := newPointerMap()
	logger := tasklog.NewLogger(os.Stdout,
		tasklog.ForceProgress(cfg.ForceProgress()),
	)
	meter := tq.NewMeter(cfg)
	meter.Logger = meter.LoggerFromEnv(cfg.Os)
	logger.Enqueue(meter)
	remote := cfg.Remote()

	// will chdir to root of working tree, if one exists
	checkout := newSingleCheckout(cfg.Git, remote)
	if sc, ok := checkout.(*singleCheckout); ok {
		sc.hardlink = pullDangerouslyHardlinkToWorktree
	}
	q := newDownloadQueue(checkout.Manifest(), remote, tq.WithProgress(meter))

	checkoutWorkers := cfg.Git.Int("lfs.concurrentcheckoutworkers", cfg.Git.Int("lfs.concurrenttransfers", lfshttp.DefaultConcurrentTransfers()))
	if checkoutWorkers < 1 {
		checkoutWorkers = 1
	}
	checkoutCh := make(chan *lfs.WrappedPointer, checkoutWorkers*2)
	var wg sync.WaitGroup
	wg.Add(checkoutWorkers)
	for i := 0; i < checkoutWorkers; i++ {
		go func() {
			defer wg.Done()
			for p := range checkoutCh {
				checkout.Run(p)
			}
		}()
	}

	gitscanner := lfs.NewGitScanner(cfg, func(p *lfs.WrappedPointer, err error) {
		if err != nil {
			LoggedError(err, tr.Tr.Get("Scanner error: %s", err))
			return
		}

		if pointers.Seen(p) {
			return
		}

		// no need to download objects that exist locally already
		lfs.LinkOrCopyFromReference(cfg, p.Oid, p.Size)
		if cfg.LFSObjectExists(p.Oid, p.Size) {
			checkoutCh <- p
			return
		}

		meter.Add(p.Size)
		tracerx.Printf("fetch %v [%v]", p.Name, p.Oid)
		pointers.Add(p)
		q.Add(downloadTransfer(p))
	})

	gitscanner.Filter = filter

	dlwatch := q.Watch()

	var dispatchWg sync.WaitGroup
	dispatchWg.Add(1)
	go func() {
		defer dispatchWg.Done()
		for t := range dlwatch {
			for _, p := range pointers.All(t.Oid) {
				checkoutCh <- p
			}
		}
	}()

	processQueue := time.Now()
	if err := gitscanner.ScanLFSFiles(ref.Sha, nil); err != nil {
		checkout.Close()
		ExitWithError(err)
	}

	meter.Start()
	q.Wait()
	dispatchWg.Wait()
	close(checkoutCh)
	wg.Wait()
	tracerx.PerformanceSince("process queue", processQueue)

	checkout.Close()

	success := true
	for _, err := range q.Errors() {
		success = false
		FullError(err)
	}

	if !success {
		c := getAPIClient()
		e := c.Endpoints.Endpoint("download", remote)
		Exit(tr.Tr.Get("Failed to fetch some objects from '%s'", e.Url))
	}

	if checkout.Skip() {
		fmt.Println(tr.Tr.Get("Skipping object checkout, Git LFS is not installed for this repository.\nConsider installing it with 'git lfs install'."))
	}
}

// tracks LFS objects being downloaded, according to their unique OIDs.
type pointerMap struct {
	pointers map[string][]*lfs.WrappedPointer
	mu       sync.Mutex
}

func newPointerMap() *pointerMap {
	return &pointerMap{pointers: make(map[string][]*lfs.WrappedPointer)}
}

func (m *pointerMap) Seen(p *lfs.WrappedPointer) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.pointers[p.Oid]; ok {
		m.pointers[p.Oid] = append(existing, p)
		return true
	}
	return false
}

func (m *pointerMap) Add(p *lfs.WrappedPointer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pointers[p.Oid] = append(m.pointers[p.Oid], p)
}

func (m *pointerMap) All(oid string) []*lfs.WrappedPointer {
	m.mu.Lock()
	defer m.mu.Unlock()
	pointers := m.pointers[oid]
	delete(m.pointers, oid)
	return pointers
}

func init() {
	RegisterCommand("pull", pullCommand, func(cmd *cobra.Command) {
		cmd.Flags().StringVarP(&includeArg, "include", "I", "", "Include a list of paths")
		cmd.Flags().StringVarP(&excludeArg, "exclude", "X", "", "Exclude a list of paths")
		cmd.Flags().BoolVar(&pullDangerouslyHardlinkToWorktree, "dangerously-hardlink-to-worktree", false, "Hardlink worktree files to the LFS object cache instead of copying. Modifying a worktree file will corrupt the cache. Ephemeral use only.")
	})
}
