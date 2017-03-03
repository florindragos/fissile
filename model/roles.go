package model

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v2"
)

const (
	boshTaskType = "bosh-task"
	boshType     = "bosh"
)

// RoleManifest represents a collection of roles
type RoleManifest struct {
	Roles         Roles          `yaml:"roles"`
	Configuration *configuration `yaml:"configuration"`

	manifestFilePath string
}

// Role represents a collection of jobs that are colocated on a container
type Role struct {
	Name              string         `yaml:"name"`
	Jobs              Jobs           `yaml:"_,omitempty"`
	EnvironScripts    []string       `yaml:"environment_scripts"`
	Scripts           []string       `yaml:"scripts"`
	PostConfigScripts []string       `yaml:"post_config_scripts"`
	Type              string         `yaml:"type,omitempty"`
	JobNameList       []*roleJob     `yaml:"jobs"`
	Configuration     *configuration `yaml:"configuration"`

	rolesManifest *RoleManifest
}

// Roles is an array of Role*
type Roles []*Role

type configuration struct {
	Templates map[string]string `yaml:"templates"`
}

type roleJob struct {
	Name        string `yaml:"name"`
	ReleaseName string `yaml:"release_name"`
}

// Len is the number of roles in the slice
func (roles Roles) Len() int {
	return len(roles)
}

// Less reports whether role at index i short sort before role at index j
func (roles Roles) Less(i, j int) bool {
	return strings.Compare(roles[i].Name, roles[j].Name) < 0
}

// Swap exchanges roles at index i and index j
func (roles Roles) Swap(i, j int) {
	roles[i], roles[j] = roles[j], roles[i]
}

// LoadRoleManifest loads a yaml manifest that details how jobs get grouped into roles
func LoadRoleManifest(manifestFilePath string, releases []*Release) (*RoleManifest, error) {
	manifestContents, err := ioutil.ReadFile(manifestFilePath)
	if err != nil {
		return nil, err
	}

	mappedReleases := map[string]*Release{}

	for _, release := range releases {
		_, ok := mappedReleases[release.Name]

		if ok {
			return nil, fmt.Errorf("Error - release %s has been loaded more than once", release.Name)
		}

		mappedReleases[release.Name] = release
	}

	rolesManifest := RoleManifest{}
	rolesManifest.manifestFilePath = manifestFilePath
	if err := yaml.Unmarshal(manifestContents, &rolesManifest); err != nil {
		return nil, err
	}

	// Remove all roles that are not of the "bosh" or "bosh-task" type
	// Default type is considered to be "bosh"
	for i := len(rolesManifest.Roles) - 1; i >= 0; i-- {
		role := rolesManifest.Roles[i]

		if role.Type != "" && role.Type != boshTaskType && role.Type != boshType {
			rolesManifest.Roles = append(rolesManifest.Roles[:i], rolesManifest.Roles[i+1:]...)
		}
	}

	if rolesManifest.Configuration == nil {
		rolesManifest.Configuration = &configuration{}
	}
	if rolesManifest.Configuration.Templates == nil {
		rolesManifest.Configuration.Templates = map[string]string{}
	}

	for _, role := range rolesManifest.Roles {
		role.rolesManifest = &rolesManifest
		role.Jobs = make(Jobs, 0, len(role.JobNameList))

		for _, roleJob := range role.JobNameList {
			release, ok := mappedReleases[roleJob.ReleaseName]

			if !ok {
				return nil, fmt.Errorf("Error - release %s has not been loaded and is referenced by job %s in role %s",
					roleJob.ReleaseName, roleJob.Name, role.Name)
			}

			job, err := release.LookupJob(roleJob.Name)
			if err != nil {
				return nil, err
			}

			role.Jobs = append(role.Jobs, job)
		}

		role.calculateRoleConfigurationTemplates()
	}

	return &rolesManifest, nil
}

// GetRoleManifestDevPackageVersion gets the aggregate signature of all the packages
func (m *RoleManifest) GetRoleManifestDevPackageVersion(extra string) (string, error) {
	// Make sure our roles are sorted, to have consistent output
	roles := append(Roles{}, m.Roles...)
	sort.Sort(roles)

	hasher := sha1.New()
	hasher.Write([]byte(extra))

	for _, role := range roles {
		version, err := role.GetRoleDevVersion()
		if err != nil {
			return "", err
		}
		hasher.Write([]byte(version))
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// GetScriptPaths returns the paths to the startup / post configgin scripts for a role
func (r *Role) GetScriptPaths() map[string]string {
	result := map[string]string{}

	for _, scriptList := range [][]string{r.EnvironScripts, r.Scripts, r.PostConfigScripts} {
		for _, script := range scriptList {
			if filepath.IsAbs(script) {
				// Absolute paths _inside_ the container; there is nothing to copy
				continue
			}
			result[script] = filepath.Join(filepath.Dir(r.rolesManifest.manifestFilePath), script)
		}
	}

	return result

}

// GetScriptSignatures returns the SHA1 of all of the script file names and contents
func (r *Role) GetScriptSignatures() (string, error) {
	hasher := sha1.New()

	i := 0
	paths := r.GetScriptPaths()
	scripts := make([]string, len(paths))

	for _, f := range paths {
		scripts[i] = f
		i += 1
	}

	sort.Strings(scripts)

	for _, filename := range scripts {
		hasher.Write([]byte(filename))

		f, err := os.Open(filename)
		if err != nil {
			return "", err
		}

		if _, err := io.Copy(hasher, f); err != nil {
			return "", err
		}

		f.Close()
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// GetTemplateSignatures returns the SHA1 of all of the templates and contents
func (r *Role) GetTemplateSignatures() (string, error) {
	hasher := sha1.New()

	i := 0
	templates := make([]string, len(r.Configuration.Templates))

	for k, v := range r.Configuration.Templates {
		templates[i] = fmt.Sprintf("%s: %s", k, v)
		i += 1
	}

	sort.Strings(templates)

	for _, template := range templates {
		hasher.Write([]byte(template))
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// GetRoleDevVersion gets the aggregate signature of all jobs and packages
func (r *Role) GetRoleDevVersion() (string, error) {
	roleSignature := ""
	var packages Packages

	// Jobs are *not* sorted because they are an array and the order may be
	// significant, in particular for bosh-task roles.
	for _, job := range r.Jobs {
		roleSignature = fmt.Sprintf("%s\n%s", roleSignature, job.SHA1)
		packages = append(packages, job.Packages...)
	}

	sort.Sort(packages)
	for _, pkg := range packages {
		roleSignature = fmt.Sprintf("%s\n%s", roleSignature, pkg.SHA1)
	}

	// Collect signatures for various script sections
	sig, err := r.GetScriptSignatures()
	if err != nil {
		return "", err
	}
	roleSignature = fmt.Sprintf("%s\n%s", roleSignature, sig)

	// If there are templates, generate signature for them
	if r.Configuration != nil && r.Configuration.Templates != nil {
		sig, err = r.GetTemplateSignatures()
		if err != nil {
			return "", err
		}
		roleSignature = fmt.Sprintf("%s\n%s", roleSignature, sig)
	}

	hasher := sha1.New()
	hasher.Write([]byte(roleSignature))
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func (r *Role) calculateRoleConfigurationTemplates() {
	if r.Configuration == nil {
		r.Configuration = &configuration{}
	}
	if r.Configuration.Templates == nil {
		r.Configuration.Templates = map[string]string{}
	}

	roleConfigs := map[string]string{}
	for k, v := range r.rolesManifest.Configuration.Templates {
		roleConfigs[k] = v
	}

	for k, v := range r.Configuration.Templates {
		roleConfigs[k] = v
	}

	r.Configuration.Templates = roleConfigs
}
