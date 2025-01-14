// Copyright (c) 2019, 2023 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/onsi/ginkgo/reporters"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestFeatureVerification(t *testing.T) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter)))
	RegisterFailHandler(Fail)
	junitReporter := reporters.NewJUnitReporter("../report/fv/fv_suite.xml")
	RunSpecsWithDefaultAndCustomReporters(t, "FV test Suite", []Reporter{junitReporter})
}
