package handler

import (
	"net/http"

	"github.com/cuihairu/ferry/server/internal/protocol"
	"github.com/gin-gonic/gin"
)

// protocolFieldOut / protocolInfoOut are the response payloads of
// GET /api/protocols (the frontend data source of the protocol pluginization):
// the protocol dropdown and config examples in the dash node form render
// dynamically from this instead of being hard-coded; formats declares the
// subscription-format support matrix (false = the protocol returns
// ErrNotSupported, so the frontend can disable the matching entry).
type protocolFieldOut struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Required bool     `json:"required"`
	Enum     []string `json:"enum,omitempty"`
	Default  any      `json:"default,omitempty"`
	Desc     string   `json:"desc,omitempty"`
}

type protocolInfoOut struct {
	Name        string             `json:"name"`
	DisplayName string             `json:"display_name"`
	DefaultPort int                `json:"default_port"`
	Example     string             `json:"example"`
	Fields      []protocolFieldOut `json:"fields"`
	Formats     map[string]bool    `json:"formats"`
}

// listProtocols returns all registered protocols (in registration order).
func (h *Handler) listProtocols(c *gin.Context) {
	all := protocol.All()
	out := make([]protocolInfoOut, 0, len(all))
	for _, p := range all {
		fields := make([]protocolFieldOut, 0, len(p.Fields))
		for _, f := range p.Fields {
			fields = append(fields, protocolFieldOut{
				Name: f.Name, Kind: string(f.Kind), Required: f.Required,
				Enum: f.Enum, Default: f.Default, Desc: f.Desc,
			})
		}
		out = append(out, protocolInfoOut{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			DefaultPort: p.DefaultPort,
			Example:     p.ExampleConfig(),
			Fields:      fields,
			Formats: map[string]bool{
				"link":     p.RenderLink != nil,
				"clash":    p.RenderClash != nil,
				"singbox":  p.RenderSingbox != nil,
				"inbound":  p.RenderSingboxInbound != nil,
				"endpoint": p.RenderSingboxEndpoint != nil,
			},
		})
	}
	c.JSON(http.StatusOK, out)
}
