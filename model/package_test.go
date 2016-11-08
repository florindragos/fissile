package model

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/hpcloud/fissile/util"

	"github.com/stretchr/testify/assert"
)

func TestPackageInfoOk(t *testing.T) {
	assert := assert.New(t)

	workDir, err := os.Getwd()
	assert.Nil(err)

	ntpReleasePath := filepath.Join(workDir, "../test-assets/ntp-release")
	ntpReleasePathBoshCache := filepath.Join(ntpReleasePath, "bosh-cache")
	release, err := NewDevRelease(ntpReleasePath, "", "", ntpReleasePathBoshCache)
	assert.Nil(err)

	assert.Equal(1, len(release.Packages))
	// Hashes taken from test-assets/ntp-release/dev_releases/ntp/ntp-2+dev.3.yml
	const ntpPkgFingerprint = "543219fbdaf6ec6f8af2956016055f2fb100d782"
	const ntpPackageSha1 = "beeacfdda7a3175a149ce0cba65f914e30db1da6"

	assert.Equal("ntp-4.2.8p2", release.Packages[0].Name)
	assert.Equal(ntpPkgFingerprint, release.Packages[0].Version)
	assert.Equal(ntpPkgFingerprint, release.Packages[0].Fingerprint)
	assert.Equal(ntpPackageSha1, release.Packages[0].SHA1)

	packagePath := filepath.Join(ntpReleasePathBoshCache, ntpPackageSha1)
	assert.Equal(packagePath, release.Packages[0].Path)

	err = util.ValidatePath(packagePath, false, "")
	assert.Nil(err)
}

func TestPackageSHA1Ok(t *testing.T) {
	assert := assert.New(t)

	workDir, err := os.Getwd()
	assert.Nil(err)

	ntpReleasePath := filepath.Join(workDir, "../test-assets/ntp-release")
	ntpReleasePathBoshCache := filepath.Join(ntpReleasePath, "bosh-cache")
	release, err := NewDevRelease(ntpReleasePath, "", "", ntpReleasePathBoshCache)
	assert.Nil(err)

	assert.Equal(1, len(release.Packages))

	assert.Nil(release.Packages[0].ValidateSHA1())
}

func TestPackageSHA1NotOk(t *testing.T) {
	assert := assert.New(t)

	workDir, err := os.Getwd()
	assert.Nil(err)

	ntpReleasePath := filepath.Join(workDir, "../test-assets/ntp-release")
	ntpReleasePathBoshCache := filepath.Join(ntpReleasePath, "bosh-cache")
	release, err := NewDevRelease(ntpReleasePath, "", "", ntpReleasePathBoshCache)
	assert.Nil(err)

	assert.Equal(1, len(release.Packages))

	// Mess up the manifest signature
	release.Packages[0].SHA1 += "foo"

	assert.NotNil(release.Packages[0].ValidateSHA1())
}

func TestPackageExtractOk(t *testing.T) {
	assert := assert.New(t)

	workDir, err := os.Getwd()
	assert.Nil(err)

	ntpReleasePath := filepath.Join(workDir, "../test-assets/ntp-release")
	ntpReleasePathBoshCache := filepath.Join(ntpReleasePath, "bosh-cache")
	release, err := NewDevRelease(ntpReleasePath, "", "", ntpReleasePathBoshCache)
	assert.Nil(err)

	assert.Equal(1, len(release.Packages))

	tempDir, err := ioutil.TempDir("", "fissile-tests")
	assert.Nil(err)
	defer os.RemoveAll(tempDir)

	packageDir, err := release.Packages[0].Extract(tempDir)
	assert.Nil(err)

	assert.Nil(util.ValidatePath(packageDir, true, ""))
	assert.Nil(util.ValidatePath(filepath.Join(packageDir, "packaging"), false, ""))
}
