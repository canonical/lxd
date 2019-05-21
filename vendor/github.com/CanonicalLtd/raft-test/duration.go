// Copyright 2017 Canonical Ltd.
//
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

package rafttest

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

// Duration is a convenience to scale the given duration according to the
// GO_RAFT_TEST_LATENCY environment variable.
func Duration(duration time.Duration) time.Duration {
	factor := 1.0
	if env := os.Getenv("GO_RAFT_TEST_LATENCY"); env != "" {
		var err error
		factor, err = strconv.ParseFloat(env, 64)
		if err != nil {
			panic(fmt.Sprintf("invalid value '%s' for GO_RAFT_TEST_LATENCY", env))
		}
	}
	return scaleDuration(duration, factor)
}

func scaleDuration(duration time.Duration, factor float64) time.Duration {
	if factor == 1.0 {
		return duration
	}

	return time.Duration((math.Ceil(float64(duration) * factor)))
}
