package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNumericPackageVersionMapping(t *testing.T) {
	for _, test := range []struct {
		upstream, channel, want string
		revision                int
	}{
		{"1.37.5", "rc", "1.37.5014", 14},
		{"1.37.5", "stable", "1.37.5515", 15},
		{"1.37.6", "rc", "1.37.6016", 16},
		{"1.38.0", "stable", "1.38.515", 15},
	} {
		got, err := mappedPackageVersion(test.upstream, test.revision, test.channel)
		if err != nil || got != test.want {
			t.Fatalf("%+v -> %q, %v", test, got, err)
		}
	}
	for _, revision := range []int{0, 500} {
		if _, err := mappedPackageVersion("1.37.5", revision, "stable"); err == nil {
			t.Fatal("out-of-range revision accepted")
		}
	}
}

func TestMigrationToNumericFormalPackage(t *testing.T) {
	formal := publishedPackage("1.37.5515")
	formal.Tag = "v1.37.5-fnos15"
	mislabelledRC := publishedPackage("1.37.5-rc14")
	for _, test := range []struct {
		current   string
		available bool
	}{
		{"1.37.5-rc14", true}, {"1.37.5", true}, {"1.37.5015", true},
		{"1.37.5515", false}, {"1.37.5516", false}, {"1.38.15", false},
	} {
		result, err := findPackageRelease([]githubRelease{formal, mislabelledRC, publishedPackage("1.37.5")}, test.current)
		if err != nil || result.Available != test.available || result.Release == nil {
			t.Fatalf("%+v -> %+v, %v", test, result, err)
		}
		if result.Release.UpstreamVersion != "1.37.5" || result.Release.Version != "1.37.5515" {
			t.Fatalf("upstream and package versions mixed: %+v", result.Release)
		}
		if result.Release.DownloadURL != "https://github.com/"+releaseRepository+"/releases/download/v1.37.5-fnos15/vaultwarden-1.37.5515-fnos.fpk" {
			t.Fatal(result.Release.DownloadURL)
		}
	}
	result, err := findPackageRelease([]githubRelease{publishedPackage("1.37.5")}, "1.37.5-rc14")
	if err != nil || result.Available {
		t.Fatal("must not offer legacy RC -> same base version as installable")
	}
	newer := publishedPackage("1.38.515")
	newer.Tag = "v1.38.0-fnos15"
	result, err = findPackageRelease([]githubRelease{formal, newer}, "1.37.5515")
	if err != nil || !result.Available || result.Release.UpstreamVersion != "1.38.0" {
		t.Fatalf("minor update with zero patch failed: %+v, %v", result, err)
	}
}

func TestPackageMetadataMustMatchInstalledFPK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "PACKAGE.json")
	data, err := json.Marshal(packageMetadata{UpstreamVersion: "1.37.5", PackageVersion: "1.37.5515", Revision: 15, Channel: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	metadata, err := loadPackageMetadata(path, "1.37.5515")
	if err != nil || metadata.UpstreamVersion != "1.37.5" {
		t.Fatalf("%+v, %v", metadata, err)
	}
	if _, err := loadPackageMetadata(path, "1.37.5"); err == nil {
		t.Fatal("mismatching installed version accepted")
	}
}
