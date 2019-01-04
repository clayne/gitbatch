package git

import (
	"errors"
	"os"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	git "gopkg.in/src-d/go-git.v4"
)

// Repository is the main entity of the application. The repository name is
// actually the name of its folder in the host's filesystem. It holds the go-git
// repository entity along with critic entites such as remote/branches and commits
type Repository struct {
	RepoID   string
	Name     string
	AbsPath  string
	ModTime  time.Time
	Repo     git.Repository
	Branch   *Branch
	Branches []*Branch
	Remote   *Remote
	Remotes  []*Remote
	Commit   *Commit
	Commits  []*Commit
	Stasheds []*StashedItem
	state    RepoState

	// TODO: move this into state
	Message string

	mutex     *sync.RWMutex
	listeners map[string][]RepositoryListener
}

// RepositoryListener is a type for listeners
type RepositoryListener func(event *RepositoryEvent) error

// RepositoryEvent is used to transfer event-related data.
// It is passed to listeners when Emit() is called
type RepositoryEvent struct {
	Name string
	Data interface{}
}

// RepoState is the state of the repository for an operation
type RepoState struct {
	State uint8
	Ready bool
}

var (
	// Available implies repo is ready for the operation
	Available = RepoState{State: 0, Ready: true}
	// Queued means repo is queued for a operation
	Queued = RepoState{State: 1, Ready: false}
	// Working means an operation is just started for this repository
	Working = RepoState{State: 2, Ready: false}
	// Paused is expected when a user interaction is required
	Paused = RepoState{State: 3, Ready: true}
	// Success is the expected outcome of the operation
	Success = RepoState{State: 4, Ready: true}
	// Fail is the unexpected outcome of the operation
	Fail = RepoState{State: 5, Ready: false}
)

const (
	// RepositoryUpdated defines the topic for an updated repository.
	RepositoryUpdated = "repository.updated"
)

// FastInitializeRepo initializes a Repository struct without its belongings.
func FastInitializeRepo(dir string) (r *Repository, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	// get status of the file
	fstat, _ := f.Stat()
	rp, err := git.PlainOpen(dir)
	if err != nil {
		return nil, err
	}
	// initialize Repository with minimum viable fields
	r = &Repository{RepoID: RandomString(8),
		Name:      fstat.Name(),
		AbsPath:   dir,
		ModTime:   fstat.ModTime(),
		Repo:      *rp,
		state:     Available,
		mutex:     &sync.RWMutex{},
		listeners: make(map[string][]RepositoryListener),
	}
	return r, nil
}

// InitializeRepo initializes a Repository struct with its belongings.
func InitializeRepo(dir string) (r *Repository, err error) {
	r, err = FastInitializeRepo(dir)
	if err != nil {
		return nil, err
	}
	// need nothing extra but loading additional components
	return r, r.loadComponents(true)
}

// loadComponents initializes the fields of a repository such as branches,
// remotes, commits etc. If reset, reload commit, remote pointers too
func (r *Repository) loadComponents(reset bool) error {
	if err := r.loadLocalBranches(); err != nil {
		return err
	}
	if err := r.loadCommits(); err != nil {
		return err
	}
	if err := r.loadRemotes(); err != nil {
		return err
	}
	if err := r.loadStashedItems(); err != nil {
		log.Warn("Cannot load stashes")
	}
	if reset {
		// handle if there is no commit, maybe?
		// set commit pointer for repository
		if len(r.Commits) > 0 {
			// select first commit
			r.Commit = r.Commits[0]
		}
		// set remote pointer for repository
		if len(r.Remotes) > 0 {
			// TODO: tend to take origin/master as default
			r.Remote = r.Remotes[0]
			// if couldn't find, its ok.
			r.Remote.SyncBranches(r.Branch.Name)
		} else {
			// if there is no remote, this project is totally useless actually
			return errors.New("There is no remote for this repository")
		}
	}
	return nil
}

// Refresh the belongings of a repositoriy, this function is called right after
// fetch/pull/merge operations
func (r *Repository) Refresh() error {
	var err error
	// error can be ignored since the file already exists when app is loading
	// if the Repository is only fast initialized, no need to refresh because
	// it won't contain its belongings
	if r.Branch == nil {
		return nil
	}
	file, _ := os.Open(r.AbsPath)
	fstat, _ := file.Stat()
	// re-initialize the go-git repository struct after supposed update
	rp, err := git.PlainOpen(r.AbsPath)
	if err != nil {
		return err
	}
	r.Repo = *rp
	// modification date may be changed
	r.ModTime = fstat.ModTime()
	if err := r.loadComponents(false); err != nil {
		return err
	}
	// we could send an event data but we don't need for this topic
	return r.Publish(RepositoryUpdated, nil)
}

// On adds new listener.
// listener is a callback function that will be called when event emits
func (r *Repository) On(event string, listener RepositoryListener) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	// add listener to the specific event topic
	r.listeners[event] = append(r.listeners[event], listener)
}

// Publish publishes the data to a certain event by its name.
func (r *Repository) Publish(eventName string, data interface{}) error {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	// let's find listeners for this event topic
	listeners, ok := r.listeners[eventName]
	if !ok {
		return nil
	}
	// now notify the listeners and channel the data
	for i := range listeners {
		event := &RepositoryEvent{
			Name: eventName,
			Data: data,
		}
		if err := listeners[i](event); err != nil {
			return err
		}
	}
	return nil
}

// State returns the state of the repository such as queued, failed etc.
func (r *Repository) State() RepoState {
	return r.state
}

// SetState sets the state of repository and sends repository updated event
func (r *Repository) SetState(state RepoState) {
	r.state = state
	// we could send an event data but we don't need for this topic
	if err := r.Publish(RepositoryUpdated, nil); err != nil {
		log.Warnf("Cannot publish on %s topic.\n", RepositoryUpdated)
	}
}

// SetMessage sets the message of status, it is used if state is Fail
func (r *Repository) SetStateMessage(msg string) {
	if r.State() == Fail {
		r.Message = msg
	}
}
