package policy

import (
	"fmt"
	"slices"
)

// ContentType is a row of the auto-ban matrix (DESIGN.md §9.2).
type ContentType string

// Content types, in the §9.2 row order.
const (
	TypeText            ContentType = "text"
	TypeLinks           ContentType = "links"
	TypeImages          ContentType = "images"
	TypeVideos          ContentType = "videos"
	TypeAudio           ContentType = "audio"
	TypeStickers        ContentType = "stickers"
	TypeDocuments       ContentType = "documents"
	TypeContacts        ContentType = "contacts"
	TypeChannelShares   ContentType = "channel_shares"
	TypeGroupInvites    ContentType = "group_invites"
	TypePolls           ContentType = "polls"
	TypeLocationsEvents ContentType = "locations_events"
	TypeStatus          ContentType = "status"
	TypeViewOnce        ContentType = "view_once"
)

var contentTypes = []ContentType{
	TypeText, TypeLinks, TypeImages, TypeVideos, TypeAudio, TypeStickers, TypeDocuments,
	TypeContacts, TypeChannelShares, TypeGroupInvites, TypePolls, TypeLocationsEvents,
	TypeStatus, TypeViewOnce,
}

// ContentTypes returns every matrix row in the §9.2 order.
func ContentTypes() []ContentType { return slices.Clone(contentTypes) }

func (t ContentType) valid() bool { return slices.Contains(contentTypes, t) }

// CellAction is the value of a matrix cell (DESIGN.md §9.2).
type CellAction string

// Cell actions.
const (
	CellAutoBan    CellAction = "auto_ban"    // delete and ban, when the evidence source is allowed
	CellAsk        CellAction = "ask"         // delete, admins decide the ban
	CellDeleteOnly CellAction = "delete_only" // delete, never ban
	CellAllow      CellAction = "allow"       // spam detection doesn't act on this type
)

// Source is the strongest kind of evidence behind a verdict (DESIGN.md §7.3).
type Source string

// Confidence sources.
const (
	SourceNone  Source = ""
	SourceKnown Source = "known" // CERTAIN: known spam, blocklists, the manual blacklist
	SourceRules Source = "rules" // heuristics; never acts alone
	SourceAI    Source = "ai"    // AI-verified
)

// Cell is one row of the auto-ban matrix: the action for spam of that type from new or
// unknown senders (DESIGN.md §9.2).
type Cell struct {
	Action        CellAction `json:"action"`
	RequireSource []Source   `json:"require_source,omitempty"` // for auto_ban; empty means [known]
	Else          CellAction `json:"else,omitempty"`           // for auto_ban when the source isn't listed; empty means ask
}

// Effective returns what the cell does for evidence from src. auto_ban only stands when src is
// one of the allowed sources (by default only known); otherwise the cell's else applies, which
// is never auto_ban.
func (c Cell) Effective(src Source) CellAction {
	if c.Action != CellAutoBan {
		return c.Action
	}
	allowed := c.RequireSource
	if len(allowed) == 0 {
		allowed = []Source{SourceKnown}
	}
	if src != SourceNone && slices.Contains(allowed, src) {
		return CellAutoBan
	}
	if c.Else == "" || c.Else == CellAutoBan {
		return CellAsk
	}
	return c.Else
}

func (c Cell) validate(key string) error {
	switch c.Action {
	case CellAutoBan:
		for _, s := range c.RequireSource {
			if err := oneOf(key+".require_source", s, SourceKnown, SourceAI); err != nil {
				return err
			}
		}
		if c.Else != "" {
			return oneOf(key+".else", c.Else, CellAsk, CellDeleteOnly, CellAllow)
		}
		return nil
	case CellAsk, CellDeleteOnly, CellAllow:
		return nil
	default:
		return oneOf(key+".action", c.Action, CellAutoBan, CellAsk, CellDeleteOnly, CellAllow)
	}
}

// String renders the cell the way §9.2 writes it: "auto_ban [known] / ask", "ask", ...
func (c Cell) String() string {
	if c.Action != CellAutoBan {
		return string(c.Action)
	}
	src := c.RequireSource
	if len(src) == 0 {
		src = []Source{SourceKnown}
	}
	els := c.Else
	if els == "" {
		els = CellAsk
	}
	return fmt.Sprintf("auto_ban %v / %s", src, els)
}

// autoKnown is "auto_ban [known] / ask", the default auto-ban cell.
func autoKnown() Cell {
	return Cell{Action: CellAutoBan, RequireSource: []Source{SourceKnown}, Else: CellAsk}
}

func cell(a CellAction) Cell { return Cell{Action: a} }

// presetMatrix returns the §9.2 grid for p.
func presetMatrix(p Preset) map[ContentType]Cell {
	m := make(map[ContentType]Cell, len(contentTypes))
	switch p {
	case PresetRelaxed:
		for _, t := range contentTypes {
			m[t] = cell(CellDeleteOnly)
		}
		for _, t := range []ContentType{TypeLinks, TypeDocuments, TypeContacts, TypeChannelShares, TypeGroupInvites} {
			m[t] = cell(CellAsk)
		}
	case PresetStrict:
		for _, t := range contentTypes {
			m[t] = autoKnown()
		}
		m[TypeViewOnce] = cell(CellAsk)
	case PresetAskAdmins:
		for _, t := range contentTypes {
			m[t] = cell(CellAsk)
		}
	default: // standard, and the base of custom
		for _, t := range contentTypes {
			m[t] = autoKnown()
		}
		for _, t := range []ContentType{TypeAudio, TypePolls, TypeLocationsEvents, TypeViewOnce} {
			m[t] = cell(CellAsk)
		}
	}
	return m
}
