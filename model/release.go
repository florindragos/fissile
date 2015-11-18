package model

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha1"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hpcloud/fissile/util"

	"gopkg.in/yaml.v2"
)

// Release represents a BOSH release
type Release struct {
	Jobs               Jobs
	Packages           Packages
	License            ReleaseLicense
	Name               string
	UncommittedChanges bool
	CommitHash         string
	Version            string
	Path               string
	Dev                bool
	DevBOSHCacheDir    string

	manifest map[interface{}]interface{}
}

const (
	jobsDir        = "jobs"
	packagesDir    = "packages"
	licenseArchive = "license.tgz"
	manifestFile   = "release.MF"
)

// yamlBinaryRegexp is the regexp used to look for the "!binary" YAML tag; see
// loadMetadata() where it is used.
var yamlBinaryRegexp = regexp.MustCompile(`([^!])!binary \|-\n`)

// NewRelease will create an instance of a BOSH release
func NewRelease(path string) (*Release, error) {
	release := &Release{
		Path:    path,
		License: ReleaseLicense{},
		Dev:     false,
	}

	if err := release.validatePathStructure(); err != nil {
		return nil, err
	}

	if err := release.loadMetadata(); err != nil {
		return nil, err
	}

	if err := release.loadLicense(); err != nil {
		return nil, err
	}

	if err := release.loadPackages(); err != nil {
		return nil, err
	}

	if err := release.loadDependenciesForPackages(); err != nil {
		return nil, err
	}

	if err := release.loadJobs(); err != nil {
		return nil, err
	}

	return release, nil
}

// GetUniqueConfigs returns all unique configs available in a release
func (r *Release) GetUniqueConfigs() map[string]*ReleaseConfig {
	result := map[string]*ReleaseConfig{}

	for _, job := range r.Jobs {
		for _, property := range job.Properties {

			if config, ok := result[property.Name]; ok {
				config.UsageCount++
				config.Jobs = append(config.Jobs, job)
			} else {
				result[property.Name] = &ReleaseConfig{
					Name:        property.Name,
					Jobs:        Jobs{job},
					UsageCount:  1,
					Description: property.Description,
				}
			}
		}
	}

	return result
}

func (r *Release) loadMetadata() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("Error trying to load release metadata from YAML manifest: %s", r)
		}
	}()

	manifestContents, err := ioutil.ReadFile(r.manifestFilePath())
	if err != nil {
		return err
	}

	// Psych (the Ruby YAML serializer) will incorrectly emit "!binary" when it means "!!binary".
	// This causes the data to be read incorrectly (not base64 decoded), which causes integrity checks to fail.
	// See https://github.com/tenderlove/psych/blob/c1decb1fef5/lib/psych/visitors/yaml_tree.rb#L309
	manifestContents = yamlBinaryRegexp.ReplaceAll(
		manifestContents,
		[]byte("$1!!binary |-\n"),
	)

	err = yaml.Unmarshal([]byte(manifestContents), &r.manifest)

	r.CommitHash = r.manifest["commit_hash"].(string)
	r.UncommittedChanges = r.manifest["uncommitted_changes"].(bool)
	r.Name = r.manifest["name"].(string)
	r.Version = r.manifest["version"].(string)

	return nil
}

// LookupPackage will find a package within a BOSH release
func (r *Release) LookupPackage(packageName string) (*Package, error) {
	for _, pkg := range r.Packages {
		if pkg.Name == packageName {
			return pkg, nil
		}
	}

	return nil, fmt.Errorf("Cannot find package %s in release", packageName)
}

// LookupJob will find a job within a BOSH release
func (r *Release) LookupJob(jobName string) (*Job, error) {
	for _, job := range r.Jobs {
		if job.Name == jobName {
			return job, nil
		}
	}

	return nil, fmt.Errorf("Cannot find job %s in release", jobName)
}

func (r *Release) loadJobs() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("Error trying to load release jobs from YAML manifest: %s", r)
		}
	}()

	jobs := r.manifest["jobs"].([]interface{})
	for _, job := range jobs {
		j, err := newJob(r, job.(map[interface{}]interface{}))
		if err != nil {
			return err
		}

		r.Jobs = append(r.Jobs, j)
	}

	return nil
}

func (r *Release) loadPackages() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("Error trying to load release packages from YAML manifest: %s", r)
		}
	}()

	packages := r.manifest["packages"].([]interface{})
	for _, pkg := range packages {
		p, err := newPackage(r, pkg.(map[interface{}]interface{}))
		if err != nil {
			return err
		}

		r.Packages = append(r.Packages, p)
	}

	return nil
}

func (r *Release) loadDependenciesForPackages() error {
	for _, pkg := range r.Packages {
		if err := pkg.loadPackageDependencies(); err != nil {
			return err
		}
	}

	return nil
}

func (r *Release) loadLicense() error {
	if licenseInfo, ok := r.manifest["license"].(map[interface{}]interface{}); ok {
		if licenseHash, ok := licenseInfo["sha1"].(string); ok {
			r.License.SHA1 = licenseHash
		}
	}

	targz, err := os.Open(r.licenseArchivePath())
	if err != nil {
		return err
	}
	defer targz.Close()

	r.License.Files = make(map[string][]byte)
	hash := sha1.New()

	err = targzIterate(
		io.TeeReader(targz, hash),
		r.licenseArchivePath(),
		func(licenseFile *tar.Reader, header *tar.Header) error {
			name := strings.ToLower(header.Name)

			if strings.Contains(name, "license") || strings.Contains(name, "notice") {
				buf, err := ioutil.ReadAll(licenseFile)
				if err != nil {
					return err
				}
				r.License.Files[filepath.Base(header.Name)] = buf
			}
			return nil
		})

	r.License.ActualSHA1 = fmt.Sprintf("%02x", hash.Sum(nil))

	return err
}

func (r *Release) validatePathStructure() error {
	if err := util.ValidatePath(r.Path, true, "release directory"); err != nil {
		return err
	}

	if err := util.ValidatePath(r.manifestFilePath(), false, "release manifest file"); err != nil {
		return err
	}

	if err := util.ValidatePath(r.packagesDirPath(), true, "packages directory"); err != nil {
		return err
	}

	if err := util.ValidatePath(r.jobsDirPath(), true, "jobs directory"); err != nil {
		return err
	}

	return nil
}

func (r *Release) licenseArchivePath() string {
	return filepath.Join(r.Path, licenseArchive)
}

func (r *Release) packagesDirPath() string {
	return filepath.Join(r.Path, packagesDir)
}

func (r *Release) jobsDirPath() string {
	return filepath.Join(r.Path, jobsDir)
}

func (r *Release) manifestFilePath() string {
	if r.Dev {
		return filepath.Join(r.getDevReleaseManifestsDir(), r.getDevReleaseManifestFilename())
	}

	return filepath.Join(r.Path, manifestFile)
}

// targzIterate iterates over the files it finds in a tar.gz file and calls a
// callback for each file encountered.
func targzIterate(targz io.Reader, filename string, fn func(*tar.Reader, *tar.Header) error) error {
	gzipReader, err := gzip.NewReader(targz)
	if err != nil {
		return fmt.Errorf("%s could not be read: %v", filename, err)
	}

	tarfile := tar.NewReader(gzipReader)
	for {
		header, err := tarfile.Next()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("%s's tar'd files failed to read: %v", filename, err)
		}

		err = fn(tarfile, header)
		if err != nil {
			return err
		}
	}
}
