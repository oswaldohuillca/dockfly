package pages

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const stagingPrefix = ".staging-"

// Storage lays out the deployments on disk:
//
//	<Root>/<siteID>/<deploymentID>/index.html
type Storage struct {
	Root   string
	Limits Limits
}

func (st *Storage) SiteDir(siteID int64) string {
	return filepath.Join(st.Root, strconv.FormatInt(siteID, 10))
}

func (st *Storage) DeploymentDir(siteID, deploymentID int64) string {
	return filepath.Join(st.SiteDir(siteID), strconv.FormatInt(deploymentID, 10))
}

// NewStaging creates a temporary directory for a new deployment. Commit or
// Discard must be called when done.
func (st *Storage) NewStaging(siteID int64) (*Staging, error) {
	siteDir := st.SiteDir(siteID)
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(siteDir, stagingPrefix+"*")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	limits := st.Limits
	if limits.MaxFiles == 0 {
		limits = DefaultLimits
	}
	return &Staging{Dir: dir, root: root, limits: limits}, nil
}

// Commit publishes the deployment at its final location. It is a rename
// within the same filesystem: atomic.
func (st *Storage) Commit(s *Staging, siteID, deploymentID int64) error {
	s.close()
	return os.Rename(s.Dir, st.DeploymentDir(siteID, deploymentID))
}

func (st *Storage) Discard(s *Staging) {
	s.close()
	os.RemoveAll(s.Dir)
}

func (st *Storage) RemoveDeployment(siteID, deploymentID int64) error {
	return os.RemoveAll(st.DeploymentDir(siteID, deploymentID))
}

func (st *Storage) RemoveSite(siteID int64) error {
	return os.RemoveAll(st.SiteDir(siteID))
}

// CleanStaging deletes the half-uploaded deployments left by a restart or a
// crash. It is called at startup.
func (st *Storage) CleanStaging() error {
	sites, err := os.ReadDir(st.Root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, site := range sites {
		if !site.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(st.Root, site.Name()))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), stagingPrefix) {
				os.RemoveAll(filepath.Join(st.Root, site.Name(), e.Name()))
			}
		}
	}
	return nil
}
