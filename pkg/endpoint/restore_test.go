// Copyright 2018 Authors of Cilium
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

package endpoint

import (
	. "gopkg.in/check.v1"
)

func (s *EndpointSuite) TesttransformEndpointForDowngrade(c *C) {
	/* Cilium 1.2 converted BoolOptions -> IntOptions. */
	e := NewEndpointWithState(42, StateReady)
	e.Options.Opts["foo"] = 0
	e.Options.Opts["bar"] = 1
	e.Options.Opts["baz"] = 2

	transformEndpointForDowngrade(e)

	c.Assert(e.DeprecatedOpts.Opts["foo"], Equals, false)
	c.Assert(e.DeprecatedOpts.Opts["bar"], Equals, true)
	_, exists := e.DeprecatedOpts.Opts["baz"]
	c.Assert(exists, Equals, false)
}
