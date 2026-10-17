package adapter

import "go.mau.fi/whatsmeow"

// Client will implement wa.WAClient on top of a whatsmeow client. It's a placeholder until
// the connector feature: it pins the whatsmeow dependency and gives the import boundary a home.
type Client struct {
	cli *whatsmeow.Client
}

// New wraps a whatsmeow client.
func New(cli *whatsmeow.Client) *Client { return &Client{cli: cli} }

// Whatsmeow returns the wrapped client.
func (c *Client) Whatsmeow() *whatsmeow.Client { return c.cli }
