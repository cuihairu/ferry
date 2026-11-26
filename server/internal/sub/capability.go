package sub

// ClientCaps is one subscription format's client-side feature surface (F-1127):
// what a client loading this target can actually be told. The matrix below is
// the single declaration point for the five delivery targets -- the delivery
// side consults it (UA negotiation picks a target first, the row says what
// that target can carry), the pack functions embody it in their schemas, and
// the tests keep the two honest against each other.
type ClientCaps struct {
	// Target is the ?target= value this row describes.
	Target string
	// Notes: the target tolerates "# " comment lines (the TOUCH-7 outage
	// escape channel travels in the body).
	Notes bool
	// UDP: a per-proxy UDP support declaration is expressible (F-1106, clash
	// family udp:). The v2ray share-link and the JSON outbounds have no such
	// field -- the declaration stays silent there.
	UDP bool
	// PacketEncoding: the Clash.Meta-only vless xudp field (G1).
	PacketEncoding bool
	// LazyGroups: url-test groups accept lazy (latency probing starts when
	// the group is selected).
	LazyGroups bool
	// RegionGroups: real proxy-groups exist (region grouping); the v2ray
	// family expresses grouping through sorting + remarks instead.
	RegionGroups bool
	// JSON: the body is plain JSON -- no comment position at all.
	JSON bool
}

// clientCaps is the matrix (F-1127): one row per delivery target. mihomo is
// the clash superset (Clash.Meta extensions), xray/sing-box are plain JSON
// outbounds arrays.
var clientCaps = map[string]ClientCaps{
	"v2ray":   {Target: "v2ray", Notes: true},
	"clash":   {Target: "clash", Notes: true, UDP: true, RegionGroups: true},
	"mihomo":  {Target: "mihomo", Notes: true, UDP: true, PacketEncoding: true, LazyGroups: true, RegionGroups: true},
	"xray":    {Target: "xray", JSON: true},
	"singbox": {Target: "singbox", JSON: true},
}

// CapsFor returns the capability row of a target; ok=false for a target the
// matrix does not know (the delivery side 400s those before packing).
func CapsFor(target string) (ClientCaps, bool) {
	c, ok := clientCaps[target]
	return c, ok
}
