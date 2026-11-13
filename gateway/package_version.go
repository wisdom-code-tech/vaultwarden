package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type packageMetadata struct {
	UpstreamVersion string `json:"upstream_version"`
	PackageVersion  string `json:"package_version"`
	Revision        int    `json:"revision"`
	Channel         string `json:"channel"`
}

func mappedPackageVersion(upstream string, revision int, channel string) (string, error) {
	parts, err := parsePackageVersion(upstream)
	if err != nil || strings.Contains(upstream, "-") {
		return "", fmt.Errorf("invalid upstream version %q", upstream)
	}
	if revision < 1 || revision > 499 {
		return "", fmt.Errorf("packaging revision must be between 1 and 499")
	}
	offset := uint64(revision)
	switch channel {
	case "stable":
		offset += 500
	case "rc":
	default:
		return "", fmt.Errorf("invalid release channel %q", channel)
	}
	if parts[2] > (^uint64(0)-offset)/1000 {
		return "", fmt.Errorf("upstream patch version exceeds package version range")
	}
	return fmt.Sprintf("%d.%d.%d", parts[0], parts[1], parts[2]*1000+offset), nil
}

func loadPackageMetadata(path, installedVersion string) (packageMetadata, error) {
	var metadata packageMetadata
	data, err := os.ReadFile(path)
	if err != nil {
		return metadata, err
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return metadata, err
	}
	expected, err := mappedPackageVersion(metadata.UpstreamVersion, metadata.Revision, metadata.Channel)
	if err != nil {
		return metadata, err
	}
	if metadata.PackageVersion != installedVersion || metadata.PackageVersion != expected {
		return metadata, fmt.Errorf("PACKAGE.json version does not match the installed FPK %s", installedVersion)
	}
	return metadata, nil
}

var formalPackageTag = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)-fnos([0-9]+)$`)

func formalReleaseVersion(tag string) (upstream, version string, err error) {
	if match := formalPackageTag.FindStringSubmatch(tag); match != nil {
		revision, parseErr := strconv.Atoi(match[2])
		if parseErr != nil {
			return "", "", parseErr
		}
		version, err = mappedPackageVersion(match[1], revision, "stable")
		return match[1], version, err
	}
	// 保留旧正式包的下载记录，但不将旧 RC -> 同上游裸版本视为可安装升级。
	version = strings.TrimPrefix(tag, "v")
	if strings.Contains(version, "-") {
		return "", "", fmt.Errorf("not a formal FPK release")
	}
	_, err = parsePackageVersion(version)
	return version, version, err
}
