/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package junit provides a junit viewer for Spyglass
package junit

import (
	"bytes"
	"sort"

	junit "github.com/joshdk/go-junit"
	"github.com/sirupsen/logrus"

	"fmt"
	"html/template"
	"path/filepath"

	"k8s.io/test-infra/prow/spyglass/lenses"
)

const (
	name     = "junit"
	title    = "JUnit"
	priority = 5
)

func init() {
	lenses.RegisterLens(Lens{})
}

// Lens is the implementation of a JUnit-rendering Spyglass lens.
type Lens struct{}

// Name returns the name.
func (lens Lens) Name() string {
	return name
}

// Title returns the title.
func (lens Lens) Title() string {
	return title
}

// Priority returns the priority.
func (lens Lens) Priority() int {
	return priority
}

// Header renders the content of <head> from template.html.
func (lens Lens) Header(artifacts []lenses.Artifact, resourceDir string) string {
	t, err := template.ParseFiles(filepath.Join(resourceDir, "template.html"))
	if err != nil {
		return fmt.Sprintf("<!-- FAILED LOADING HEADER: %v -->", err)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "header", nil); err != nil {
		return fmt.Sprintf("<!-- FAILED EXECUTING HEADER TEMPLATE: %v -->", err)
	}
	return buf.String()
}

// Callback does nothing.
func (lens Lens) Callback(artifacts []lenses.Artifact, resourceDir string, data string) string {
	return ""
}

// TestResult holds data about a test extracted from junit output
type TestResult struct {
	Junit junit.Test
	Link  string
}

// Body renders the <body> for JUnit tests
func (lens Lens) Body(artifacts []lenses.Artifact, resourceDir string, data string) string {
	type testResults struct {
		junit []junit.Test
		link  string
		path  string
		err   error
	}
	resultChan := make(chan testResults)
	for _, artifact := range artifacts {
		go func(artifact lenses.Artifact) {
			result := testResults{
				link: artifact.CanonicalLink(),
				path: artifact.JobPath(),
			}
			var contents []byte
			contents, result.err = artifact.ReadAll()
			if result.err != nil {
				logrus.WithError(result.err).Error("Error reading artifact")
				resultChan <- result
				return
			}
			var suites []junit.Suite
			suites, result.err = junit.Ingest(contents)
			if result.err != nil {
				logrus.WithError(result.err).Error("Error parsing junit file.")
				resultChan <- result
				return
			}
			for _, suite := range suites {
				for _, test := range suite.Tests {
					result.junit = append(result.junit, test)
				}
			}
			resultChan <- result
		}(artifact)
	}
	results := make([]testResults, 0, len(artifacts))
	for range artifacts {
		results = append(results, <-resultChan)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].path < results[j].path })

	jvd := struct {
		NumTests int
		Passed   []TestResult
		Failed   []TestResult
		Skipped  []TestResult
	}{}
	for _, result := range results {
		if result.err != nil {
			continue
		}
		for _, test := range result.junit {
			if test.Status == "failed" {
				jvd.Failed = append(jvd.Failed, TestResult{
					Junit: test,
					Link:  result.link,
				})
			} else if test.Status == "skipped" {
				jvd.Skipped = append(jvd.Skipped, TestResult{
					Junit: test,
					Link:  result.link,
				})
			} else if test.Status == "passed" {
				jvd.Passed = append(jvd.Passed, TestResult{
					Junit: test,
					Link:  result.link,
				})
			} else {
				logrus.WithField("status", test.Status).Error("invalid JUnit test status string")
			}
		}
	}

	if jvd.NumTests = len(jvd.Passed) + len(jvd.Failed) + len(jvd.Skipped); jvd.NumTests == 0 {
		return "Found no valid JUnit test results"
	}

	junitTemplate, err := template.ParseFiles(filepath.Join(resourceDir, "template.html"))
	if err != nil {
		logrus.WithError(err).Error("Error executing template.")
		return fmt.Sprintf("Failed to load template file: %v", err)
	}

	var buf bytes.Buffer
	if err := junitTemplate.ExecuteTemplate(&buf, "body", jvd); err != nil {
		logrus.WithError(err).Error("Error executing template.")
	}

	return buf.String()
}
