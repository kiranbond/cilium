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

package policy

import (
	"bytes"

	"github.com/cilium/cilium/pkg/checker"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/policy/api"

	"github.com/op/go-logging"
	. "gopkg.in/check.v1"
)

var (
	fooSelector      = api.NewESFromLabels(labels.ParseSelectLabel("foo"))
	barSelector      = api.NewESFromLabels(labels.ParseSelectLabel("bar"))
	hostSelector     = api.ReservedEndpointSelectors[labels.IDNameHost]
	fooSelectorSlice = []api.EndpointSelector{
		fooSelector,
	}
	toBar = &SearchContext{To: labels.ParseSelectLabelArray("bar")}
	toFoo = &SearchContext{To: labels.ParseSelectLabelArray("foo")}
)

// Tests in this file:
//
// How to read this table:
//   Case:  The test / subtest number.
//   L3:    Matches at L3 for rule 1,  followed by rule 2.
//   L4:    Matches at L4.
//   L7:    Rules at L7 for rule 1, followed by rule 2.
//   Notes: Extra information about the test.
//
// +-----+-----------------+----------+-----------------+------------------------------------------------------+
// |Case | L3 (1, 2) match | L4 match | L7 match (1, 2) | Notes                                                |
// +=====+=================+==========+=================+======================================================+
// |  1A |      *, *       |  80/TCP  |      *, *       | Allow all communication on the specified port        |
// |  1B |      *, *       |  80/TCP  |      *, *       | Same as 1A, with implicit L3 wildcards               |
// |  2A |      *, *       |  80/TCP  |   *, "GET /"    | Rule 1 shadows rule 2                                |
// |  2B |      *, *       |  80/TCP  |   "GET /", *    | Same as 2A, but import in reverse order              |
// |  3  |      *, *       |  80/TCP  | "GET /","GET /" | Exactly duplicate rules (HTTP)                       |
// |  4  |      *, *       | 9092/TCP |   "foo","foo"   | Exactly duplicate rules (Kafka)                      |
// |  5A |      *, *       |  80/TCP  |  "foo","GET /"  | Rules with conflicting L7 parser                     |
// |  5B |      *, *       |  80/TCP  |  "GET /","foo"  | Same as 5A, but import in reverse order              |
// |  6A |   "id=a", *     |  80/TCP  |      *, *       | Rule 2 is a superset of rule 1                       |
// |  6B |   *, "id=a"     |  80/TCP  |      *, *       | Same as 6A, but import in reverse order              |
// |  7A |   "id=a", *     |  80/TCP  |   "GET /", *    | All traffic is allowed; traffic to A goes via proxy  |
// |  7B |   *, "id=a"     |  80/TCP  |   *, "GET /"    | Same as 7A, but import in reverse order              |
// |  8A |   "id=a", *     |  80/TCP  | "GET /","GET /" | Rule 2 is the same as rule 1, except matching all L3 |
// |  8B |   *, "id=a"     |  80/TCP  | "GET /","GET /" | Same as 8A, but import in reverse order              |
// |  9A |   "id=a", *     |  80/TCP  |  "foo","GET /"  | Rules with conflicting L7 parser (+L3 match)         |
// |  9B |   *, "id=a"     |  80/TCP  |  "GET /","foo"  | Same as 9A, but import in reverse order              |
// | 10  | "id=a", "id=c"  |  80/TCP  | "GET /","GET /" | Allow at L7 for two distinct labels (disjoint set)   |
// | 11  | "id=a", "id=c"  |  80/TCP  |      *, *       | Allow at L4 for two distinct labels (disjoint set)   |
// | 12  |     "id=a",     |  80/TCP  |     "GET /"     | Configure to allow localhost traffic always          |
// +-----+-----------------+----------+-----------------+------------------------------------------------------+

// Case 1: allow all at L3 in both rules, and all at L7 (duplicate rule).
func (ds *PolicyTestSuite) TestMergeAllowAllL3AndAllowAllL7(c *C) {
	// Case 1A: Specify WildcardEndpointSelector explicitly.
	repo := parseAndAddRules(c, api.Rules{&api.Rule{
		EndpointSelector: endpointSelectorA,
		Ingress: []api.IngressRule{
			{
				FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
			{
				FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
		},
	}})

	buffer := new(bytes.Buffer)
	ctx := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = logging.NewLogBackend(buffer, "", 0)

	l4IngressPolicy, err := repo.ResolveL4IngressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok := (*l4IngressPolicy)["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(filter.Endpoints.SelectsAllEndpoints(), Equals, true)

	c.Assert(filter.L7Parser, Equals, ParserTypeNone)
	c.Assert(len(filter.L7RulesPerEp), Equals, 0)

	// Case1B: implicitly wildcard all endpoints.
	repo = parseAndAddRules(c, api.Rules{&api.Rule{
		EndpointSelector: endpointSelectorA,
		Ingress: []api.IngressRule{
			{
				FromEndpoints: []api.EndpointSelector{},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
			{
				FromEndpoints: []api.EndpointSelector{},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
		},
	}})

	buffer = new(bytes.Buffer)
	ctx = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = logging.NewLogBackend(buffer, "", 0)

	l4IngressPolicy, err = repo.ResolveL4IngressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok = (*l4IngressPolicy)["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(filter.Endpoints.SelectsAllEndpoints(), Equals, true)

	c.Assert(filter.L7Parser, Equals, ParserTypeNone)
	c.Assert(len(filter.L7RulesPerEp), Equals, 0)
}

// Case 2: allow all at L3 in both rules. Allow all in one L7 rule, but second
// rule restricts at L7. Because one L7 rule allows at L7, all traffic is allowed
// at L7, but still redirected at the proxy.
// Should resolve to one rule.
func (ds *PolicyTestSuite) TestMergeAllowAllL3AndShadowedL7(c *C) {
	rule1 := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctx := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = logging.NewLogBackend(buffer, "", 0)

	ingressState := traceState{}
	res, err := rule1.resolveL4IngressPolicy(&ctx, &ingressState, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))

	c.Log(buffer)

	// The expected policy contains the L7 Rules below, but in practice
	// when the policy is being resolved and sent to the proxy, it actually
	// allows all at L7, based on the first API rule imported above. We
	// just set the expected set of L7 rules below to include this to match
	// the current implementation.
	expected := NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:      80,
		Protocol:  api.ProtoTCP,
		U8Proto:   6,
		Endpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:  "http",
		L7RulesPerEp: L7DataMap{
			api.WildcardEndpointSelector: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(ingressState.selectedRules, Equals, 1)
	c.Assert(ingressState.matchedRules, Equals, 0)

	// Case 2B: Flip order of case 2A so that rule being merged with is different
	// than rule being consumed.
	repo := parseAndAddRules(c, api.Rules{&api.Rule{
		EndpointSelector: endpointSelectorA,
		Ingress: []api.IngressRule{
			{
				FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
					Rules: &api.L7Rules{
						HTTP: []api.PortRuleHTTP{
							{Method: "GET", Path: "/"},
						},
					},
				}},
			},
			{
				FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
				ToPorts: []api.PortRule{{
					Ports: []api.PortProtocol{
						{Port: "80", Protocol: api.ProtoTCP},
					},
				}},
			},
		},
	}})

	buffer = new(bytes.Buffer)
	ctx = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctx.Logging = logging.NewLogBackend(buffer, "", 0)

	l4IngressPolicy, err := repo.ResolveL4IngressPolicy(&ctx)
	c.Assert(err, IsNil)

	c.Log(buffer)

	filter, ok := (*l4IngressPolicy)["80/TCP"]
	c.Assert(ok, Equals, true)
	c.Assert(filter.Port, Equals, 80)
	c.Assert(filter.Ingress, Equals, true)

	c.Assert(filter.Endpoints.SelectsAllEndpoints(), Equals, true)

	c.Assert(filter.L7Parser, Equals, ParserTypeHTTP)
	c.Assert(len(filter.L7RulesPerEp), Equals, 1)
}

// Case 3: allow all at L3 in both rules. Both rules have same parser type and
// same API resource specified at L7 for HTTP.
func (ds *PolicyTestSuite) TestMergeIdenticalAllowAllL3AndRestrictedL7HTTP(c *C) {
	identicalHTTPRule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	expected := NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:      80,
		Protocol:  api.ProtoTCP,
		U8Proto:   6,
		Endpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:  ParserTypeHTTP,
		L7RulesPerEp: L7DataMap{
			api.WildcardEndpointSelector: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	state := traceState{}
	res, err := identicalHTTPRule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	state = traceState{}
	res, err = identicalHTTPRule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)
}

// Case 4: identical allow all at L3 with identical restrictions on Kafka.
func (ds *PolicyTestSuite) TestMergeIdenticalAllowAllL3AndRestrictedL7Kafka(c *C) {

	identicalKafkaRule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "9092", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []api.PortRuleKafka{
								{Topic: "foo"},
							},
						},
					}},
				},
				{
					FromEndpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "9092", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []api.PortRuleKafka{
								{Topic: "foo"},
							},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected := NewL4Policy()
	expected.Ingress["9092/TCP"] = L4Filter{
		Port:      9092,
		Protocol:  api.ProtoTCP,
		U8Proto:   6,
		Endpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:  ParserTypeKafka,
		L7RulesPerEp: L7DataMap{
			api.WildcardEndpointSelector: api.L7Rules{
				Kafka: []api.PortRuleKafka{{Topic: "foo"}},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	state := traceState{}
	res, err := identicalKafkaRule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	state = traceState{}
	res, err = identicalKafkaRule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)

}

// Case 5: use conflicting protocols on the same port in different rules. This
// is not supported, so return an error.
func (ds *PolicyTestSuite) TestMergeIdenticalAllowAllL3AndMismatchingParsers(c *C) {

	// Case 5A: Kafka first, HTTP second.
	conflictingParsersRule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []api.PortRuleKafka{
								{Topic: "foo"},
							},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	state := traceState{}
	res, err := conflictingParsersRule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, Not(IsNil))
	c.Assert(res, IsNil)

	// Case 5B: HTTP first, Kafka second.
	conflictingParsersRule = &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
				{
					FromEndpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []api.PortRuleKafka{
								{Topic: "foo"},
							},
						},
					}},
				},
			},
		}}

	buffer = new(bytes.Buffer)
	ctxToA = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	state = traceState{}
	res, err = conflictingParsersRule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, Not(IsNil))
	c.Assert(res, IsNil)

	// Case 5B+: HTTP first, generic L7 second.
	conflictingParsersIngressRule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
				{
					FromEndpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							L7Proto: "testing",
							L7: []api.PortRuleL7{
								{"method": "PUT", "path": "/Foo"},
							},
						},
					}},
				},
			},
		}}

	buffer = new(bytes.Buffer)
	ctxToA = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	err = conflictingParsersIngressRule.Sanitize()
	c.Assert(err, IsNil)

	state = traceState{}
	res, err = conflictingParsersIngressRule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, Not(IsNil))
	c.Assert(res, IsNil)

	// Case 5B++: generic L7 without rules first, HTTP second.
	conflictingParsersEgressRule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Egress: []api.EgressRule{
				{
					ToEndpoints: []api.EndpointSelector{endpointSelectorC},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							L7Proto: "testing",
						},
					}},
				},
				{
					ToEndpoints: []api.EndpointSelector{endpointSelectorC},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	buffer = new(bytes.Buffer)
	ctxAToC := SearchContext{From: labelsA, To: labelsC, Trace: TRACE_VERBOSE}
	ctxAToC.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	err = conflictingParsersEgressRule.Sanitize()
	c.Assert(err, IsNil)

	state = traceState{}
	res, err = conflictingParsersEgressRule.resolveL4EgressPolicy(&ctxAToC, &state, NewL4Policy(), nil)
	c.Log(buffer)
	c.Assert(err, Not(IsNil))
	c.Assert(res, IsNil)
}

// Case 6: allow all at L3/L7 in one rule, and select an endpoint and allow all on L7
// in another rule. Should resolve to just allowing all on L3/L7 (first rule
// shadows the second).
func (ds *PolicyTestSuite) TestL3RuleShadowedByL3AllowAll(c *C) {
	// Case 6A: Specify WildcardEndpointSelector explicitly.
	shadowRule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected := NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:             80,
		Protocol:         api.ProtoTCP,
		U8Proto:          6,
		Endpoints:        api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:         ParserTypeNone,
		L7RulesPerEp:     L7DataMap{},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	state := traceState{}
	res, err := shadowRule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	state = traceState{}
	res, err = shadowRule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)

	// Case 6B: Reverse the ordering of the rules. Result should be the same.
	shadowRule = &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		}}

	buffer = new(bytes.Buffer)
	ctxToA = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected = NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:             80,
		Protocol:         api.ProtoTCP,
		U8Proto:          6,
		Endpoints:        api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:         ParserTypeNone,
		L7RulesPerEp:     L7DataMap{},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	state = traceState{}
	res, err = shadowRule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	state = traceState{}
	res, err = shadowRule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)
}

// Case 7: allow all at L3/L7 in one rule, and in another rule, select an endpoint
// which restricts on L7. Should resolve to just allowing all on L3/L7 (first rule
// shadows the second), but setting traffic to the HTTP proxy.
func (ds *PolicyTestSuite) TestL3RuleWithL7RulePartiallyShadowedByL3AllowAll(c *C) {
	// Case 7A: selects specific endpoint with L7 restrictions rule first, then
	// rule which selects all endpoints and allows all on L7. Net result sets
	// parser type to whatever is in first rule, but without the restriction
	// on L7.
	shadowRule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected := NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:      80,
		Protocol:  api.ProtoTCP,
		U8Proto:   6,
		Endpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:  ParserTypeHTTP,
		L7RulesPerEp: L7DataMap{
			endpointSelectorA: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	state := traceState{}
	res, err := shadowRule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	state = traceState{}
	res, err = shadowRule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)

	// Case 7B: selects all endpoints and allows all on L7, then selects specific
	// endpoint with L7 restrictions rule. Net result sets  parser type to whatever
	// is in first rule, but without the restriction on L7.
	shadowRule = &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	buffer = new(bytes.Buffer)
	ctxToA = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected = NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:      80,
		Protocol:  api.ProtoTCP,
		U8Proto:   6,
		Endpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:  ParserTypeHTTP,
		L7RulesPerEp: L7DataMap{
			endpointSelectorA: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	state = traceState{}
	res, err = shadowRule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	state = traceState{}
	res, err = shadowRule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)
}

// Case 8: allow all at L3 and restricts on L7 in one rule, and in another rule,
// select an endpoint which restricts the same as the first rule on L7.
// Should resolve to just allowing all on L3, but restricting on L7 for both
// wildcard and the specified endpoint.
func (ds *PolicyTestSuite) TestL3RuleWithL7RuleShadowedByL3AllowAll(c *C) {

	// Case 8A: selects specific endpoint with L7 restrictions rule first, then
	// rule which selects all endpoints and restricts on the same resource on L7.
	// L7RulesPerEp contains entries for both endpoints selected in each rule
	// on L7 restriction.
	case8Rule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected := NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:      80,
		Protocol:  api.ProtoTCP,
		U8Proto:   6,
		Endpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:  ParserTypeHTTP,
		L7RulesPerEp: L7DataMap{
			api.WildcardEndpointSelector: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
			endpointSelectorA: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	state := traceState{}
	res, err := case8Rule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	state = traceState{}
	res, err = case8Rule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)

	// Case 8B: first insert rule which selects all endpoints and restricts on
	// the same resource on L7. Then, insert rule which  selects specific endpoint
	// with L7 restrictions rule. L7RulesPerEp contains entries for both
	// endpoints selected in each rule on L7 restriction.
	case8Rule = &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	buffer = new(bytes.Buffer)
	ctxToA = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected = NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:      80,
		Protocol:  api.ProtoTCP,
		U8Proto:   6,
		Endpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:  ParserTypeHTTP,
		L7RulesPerEp: L7DataMap{
			api.WildcardEndpointSelector: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
			endpointSelectorA: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	state = traceState{}
	res, err = case8Rule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	state = traceState{}
	res, err = case8Rule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)
}

// Case 9: allow all at L3 and restricts on L7 in one rule, and in another rule,
// select an endpoint which restricts on different L7 protocol.
// Should fail as cannot have conflicting parsers on same port.
func (ds *PolicyTestSuite) TestL3SelectingEndpointAndL3AllowAllMergeConflictingL7(c *C) {

	// Case 9A: Kafka first, then HTTP.
	conflictingL7Rule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []api.PortRuleKafka{
								{Topic: "foo"},
							},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	state := traceState{}
	res, err := conflictingL7Rule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, Not(IsNil))
	c.Assert(res, IsNil)

	state = traceState{}
	res, err = conflictingL7Rule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)

	// Case 9B: HTTP first, then Kafka.
	conflictingL7Rule = &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							Kafka: []api.PortRuleKafka{
								{Topic: "foo"},
							},
						},
					}},
				},
			},
		}}

	buffer = new(bytes.Buffer)
	ctxToA = SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	state = traceState{}
	res, err = conflictingL7Rule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, Not(IsNil))
	c.Assert(res, IsNil)

	state = traceState{}
	res, err = conflictingL7Rule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)
}

// Case 10: restrict same path / method on L7 in both rules,
// but select different endpoints in each rule.
func (ds *PolicyTestSuite) TestMergingWithDifferentEndpointsSelectedAllowSameL7(c *C) {

	selectDifferentEndpointsRestrictL7 := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected := NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:      80,
		Protocol:  api.ProtoTCP,
		U8Proto:   6,
		Endpoints: api.EndpointSelectorSlice{endpointSelectorA, endpointSelectorC},
		L7Parser:  ParserTypeHTTP,
		L7RulesPerEp: L7DataMap{
			endpointSelectorC: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
			endpointSelectorA: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	state := traceState{}
	res, err := selectDifferentEndpointsRestrictL7.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	buffer = new(bytes.Buffer)
	ctxToC := SearchContext{To: labelsC, Trace: TRACE_VERBOSE}
	ctxToC.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	state = traceState{}
	res, err = selectDifferentEndpointsRestrictL7.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)
}

// Case 11: allow all on L7 in both rules, but select different endpoints in each rule.
func (ds *PolicyTestSuite) TestMergingWithDifferentEndpointSelectedAllowAllL7(c *C) {

	selectDifferentEndpointsAllowAllL7 := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorA},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
				{
					FromEndpoints: []api.EndpointSelector{endpointSelectorC},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected := NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:             80,
		Protocol:         api.ProtoTCP,
		U8Proto:          6,
		Endpoints:        api.EndpointSelectorSlice{endpointSelectorA, endpointSelectorC},
		L7Parser:         ParserTypeNone,
		L7RulesPerEp:     L7DataMap{},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil, nil},
	}

	state := traceState{}
	res, err := selectDifferentEndpointsAllowAllL7.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	buffer = new(bytes.Buffer)
	ctxToC := SearchContext{To: labelsC, Trace: TRACE_VERBOSE}
	ctxToC.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	state = traceState{}
	res, err = selectDifferentEndpointsAllowAllL7.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)
}

// Case 12: allow all at L3 in one rule with restrictions at L7. Determine that
//          the host should always be allowed. From Host should go to proxy
//          allow all; other L3 should restrict at L7 in a separate filter.
func (ds *PolicyTestSuite) TestAllowingLocalhostShadowsL7(c *C) {

	// This test checks that when the AllowLocalhost=always option is
	// enabled, we always wildcard the host at L7. That means we need to
	// set the option in the config, and of course clean up afterwards so
	// that this test doesn't affect subsequent tests.
	oldLocalhostOpt := option.Config.AllowLocalhost
	option.Config.AllowLocalhost = option.AllowLocalhostAlways
	defer func() { option.Config.AllowLocalhost = oldLocalhostOpt }()

	rule := &rule{
		Rule: api.Rule{
			EndpointSelector: endpointSelectorA,
			Ingress: []api.IngressRule{
				{
					FromEndpoints: []api.EndpointSelector{api.WildcardEndpointSelector},
					ToPorts: []api.PortRule{{
						Ports: []api.PortProtocol{
							{Port: "80", Protocol: api.ProtoTCP},
						},
						Rules: &api.L7Rules{
							HTTP: []api.PortRuleHTTP{
								{Method: "GET", Path: "/"},
							},
						},
					}},
				},
			},
		}}

	buffer := new(bytes.Buffer)
	ctxToA := SearchContext{To: labelsA, Trace: TRACE_VERBOSE}
	ctxToA.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	expected := NewL4Policy()
	expected.Ingress["80/TCP"] = L4Filter{
		Port:      80,
		Protocol:  api.ProtoTCP,
		U8Proto:   6,
		Endpoints: api.EndpointSelectorSlice{api.WildcardEndpointSelector},
		L7Parser:  ParserTypeHTTP,
		L7RulesPerEp: L7DataMap{
			api.WildcardEndpointSelector: api.L7Rules{
				HTTP: []api.PortRuleHTTP{{Path: "/", Method: "GET"}},
			},
			hostSelector: api.L7Rules{}, // Empty => Allow all
		},
		Ingress:          true,
		DerivedFromRules: labels.LabelArrayList{nil},
	}

	state := traceState{}
	res, err := rule.resolveL4IngressPolicy(&ctxToA, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, Not(IsNil))
	c.Assert(*res, checker.DeepEquals, *expected)
	c.Assert(state.selectedRules, Equals, 1)
	c.Assert(state.matchedRules, Equals, 0)

	// Endpoints not selected by the rule should not match the rule.
	buffer = new(bytes.Buffer)
	ctxToC := SearchContext{To: labelsC, Trace: TRACE_VERBOSE}
	ctxToC.Logging = logging.NewLogBackend(buffer, "", 0)
	c.Log(buffer)

	state = traceState{}
	res, err = rule.resolveL4IngressPolicy(toFoo, &state, NewL4Policy(), nil)
	c.Assert(err, IsNil)
	c.Assert(res, IsNil)
	c.Assert(state.selectedRules, Equals, 0)
	c.Assert(state.matchedRules, Equals, 0)
}
