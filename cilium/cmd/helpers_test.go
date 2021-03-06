package cmd

import (
	"bytes"
	"sort"
	"strconv"
	"testing"

	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/labels"
	"github.com/cilium/cilium/pkg/maps/policymap"
	"github.com/cilium/cilium/pkg/u8proto"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

type CMDHelpersSuite struct{}

var _ = Suite(&CMDHelpersSuite{})

func (s *CMDHelpersSuite) TestExpandNestedJSON(c *C) {
	buf := bytes.NewBufferString("not json at all")
	_, err := expandNestedJSON(*buf)
	c.Assert(err, IsNil)

	buf = bytes.NewBufferString(`{\n\"escapedJson\": \"foo\"}`)
	_, err = expandNestedJSON(*buf)
	c.Assert(err, IsNil)

	buf = bytes.NewBufferString(`nonjson={\n\"escapedJson\": \"foo\"}`)
	_, err = expandNestedJSON(*buf)
	c.Assert(err, IsNil)

	buf = bytes.NewBufferString(`nonjson:morenonjson={\n\"escapedJson\": \"foo\"}`)
	_, err = expandNestedJSON(*buf)
	c.Assert(err, IsNil)

	buf = bytes.NewBufferString(`{"foo": ["{\n  \"port\": 8080,\n  \"protocol\": \"TCP\"\n}"]}`)
	_, err = expandNestedJSON(*buf)
	c.Assert(err, IsNil)

	buf = bytes.NewBufferString(`"foo": [
  "bar:baz/alice={\"bob\":{\"charlie\":4}}\n"
]`)
	_, err = expandNestedJSON(*buf)
	c.Assert(err, IsNil)
}

func (s *CMDHelpersSuite) TestParseTrafficString(c *C) {

	validIngressCases := []string{"ingress", "Ingress", "InGrEss"}
	validEgressCases := []string{"egress", "Egress", "EGrEss"}

	invalidStr := "getItDoneMan"

	for _, validCase := range validIngressCases {
		ingressDir, err := parseTrafficString(validCase)
		c.Assert(ingressDir, Equals, policymap.Ingress)
		c.Assert(err, IsNil)
	}

	for _, validCase := range validEgressCases {
		egressDir, err := parseTrafficString(validCase)
		c.Assert(egressDir, Equals, policymap.Egress)
		c.Assert(err, IsNil)
	}

	invalid, err := parseTrafficString(invalidStr)
	c.Assert(invalid, Equals, policymap.Invalid)
	c.Assert(err, Not(IsNil))

}

func (s *CMDHelpersSuite) TestParsePolicyUpdateArgsHelper(c *C) {
	sortProtos := func(ints []uint8) {
		sort.Slice(ints, func(i, j int) bool {
			return ints[i] < ints[j]
		})
	}

	allProtos := []uint8{}
	for _, proto := range u8proto.ProtoIDs {
		allProtos = append(allProtos, uint8(proto))
	}

	tests := []struct {
		args             []string
		invalid          bool
		endpointID       string
		trafficDirection policymap.TrafficDirection
		peerLbl          uint32
		port             uint16
		protos           []uint8
	}{
		{
			args:             []string{labels.IDNameHost, "ingress", "12345"},
			invalid:          false,
			endpointID:       "reserved_" + strconv.Itoa(int(identity.ReservedIdentityHost)),
			trafficDirection: policymap.Ingress,
			peerLbl:          12345,
			port:             0,
			protos:           []uint8{0},
		},
		{
			args:             []string{"123", "egress", "12345", "1/tcp"},
			invalid:          false,
			endpointID:       "123",
			trafficDirection: policymap.Egress,
			peerLbl:          12345,
			port:             1,
			protos:           []uint8{uint8(u8proto.TCP)},
		},
		{
			args:             []string{"123", "ingress", "12345", "1"},
			invalid:          false,
			endpointID:       "123",
			trafficDirection: policymap.Ingress,
			peerLbl:          12345,
			port:             1,
			protos:           allProtos,
		},
		{
			// Invalid traffic direction.
			args:    []string{"123", "invalid", "12345"},
			invalid: true,
		},
		{
			// Invalid protocol.
			args:    []string{"123", "invalid", "1/udt"},
			invalid: true,
		},
	}

	for _, tt := range tests {
		args, err := parsePolicyUpdateArgsHelper(tt.args)

		if tt.invalid {
			c.Assert(err, NotNil)
		} else {
			c.Assert(err, IsNil)

			c.Assert(args.endpointID, Equals, tt.endpointID)
			c.Assert(args.trafficDirection, Equals, tt.trafficDirection)
			c.Assert(args.label, Equals, tt.peerLbl)
			c.Assert(args.port, Equals, tt.port)

			sortProtos(args.protocols)
			sortProtos(tt.protos)
			c.Assert(args.protocols, DeepEquals, tt.protos)
		}
	}
}
