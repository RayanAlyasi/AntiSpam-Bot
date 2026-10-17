package policy

import (
	"slices"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

func TestTypeOf(t *testing.T) {
	tests := []struct {
		name string
		e    envelope.Envelope
		want ContentType
	}{
		{"plain text", envelope.Envelope{Kind: envelope.KindText}, TypeText},
		{"text with a link", envelope.Envelope{Kind: envelope.KindText, URLs: []envelope.URLRef{{URL: "https://x.com"}}}, TypeLinks},
		{"image with a link caption", envelope.Envelope{Kind: envelope.KindImage, URLs: []envelope.URLRef{{URL: "x.com"}}}, TypeImages},
		{"video note", envelope.Envelope{Kind: envelope.KindPTV}, TypeVideos},
		{"voice", envelope.Envelope{Kind: envelope.KindVoice}, TypeAudio},
		{"sticker pack", envelope.Envelope{Kind: envelope.KindStickerPack}, TypeStickers},
		{"document", envelope.Envelope{Kind: envelope.KindDocument}, TypeDocuments},
		{"contact", envelope.Envelope{Kind: envelope.KindContact}, TypeContacts},
		{"text carrying an invite link", envelope.Envelope{Kind: envelope.KindText, Invites: []envelope.InviteRef{{Code: "abc"}}}, TypeGroupInvites},
		{"channel invite", envelope.Envelope{Kind: envelope.KindChannelInvite}, TypeChannelShares},
		{"forwarded from a channel", envelope.Envelope{Kind: envelope.KindText, Channels: []envelope.ChannelRef{{JID: "1@newsletter"}}}, TypeChannelShares},
		{"poll", envelope.Envelope{Kind: envelope.KindPoll}, TypePolls},
		{"live location", envelope.Envelope{Kind: envelope.KindLiveLocation}, TypeLocationsEvents},
		{"event", envelope.Envelope{Kind: envelope.KindEvent}, TypeLocationsEvents},
		{"status share", envelope.Envelope{Kind: envelope.KindImage, Status: &envelope.StatusRef{ForwardedFromStatus: true}}, TypeStatus},
		{"group status post", envelope.Envelope{Kind: envelope.KindVideo, Status: &envelope.StatusRef{IsGroupStatus: true}}, TypeStatus},
		{"view-once image", envelope.Envelope{Kind: envelope.KindImage, IsViewOnce: true}, TypeViewOnce},
		{"unknown kind", envelope.Envelope{Kind: envelope.KindUnknown}, TypeText},
	}
	for _, tt := range tests {
		if got := TypeOf(tt.e); got != tt.want {
			t.Errorf("%s: TypeOf = %s, want %s (§9.2 row)", tt.name, got, tt.want)
		}
	}
}

func TestFactsOf(t *testing.T) {
	e := envelope.Envelope{
		Kind:            envelope.KindDocument,
		ForwardingScore: 7,
		URLs:            []envelope.URLRef{{URL: "https://Bit.ly:443/x"}, {URL: "example.com/a"}, {URL: "::bad"}},
		Texts:           []envelope.TextSurface{{Source: "doc_file_name", Raw: "invoice.pdf.exe"}, {Source: envelope.SourceCaption, Raw: "hi"}},
		Media:           []envelope.MediaRef{{Type: envelope.KindDocument, Mimetype: "application/vnd.android.package-archive"}, {Type: envelope.KindImage, Mimetype: "image/jpeg"}},
		Status:          &envelope.StatusRef{IsStatusMention: true},
	}
	f := FactsOf(e)
	if !slices.Equal(f.LinkHosts, []string{"Bit.ly:443", "example.com"}) {
		t.Errorf("LinkHosts = %v, want the hosts of the parseable links", f.LinkHosts)
	}
	if !f.HasDocument || !slices.Equal(f.Documents, []string{"invoice.pdf.exe"}) || !slices.Equal(f.DocumentMimetypes, []string{"application/vnd.android.package-archive"}) {
		t.Errorf("documents = %v %v %v, want the file name and the document's mimetype only", f.HasDocument, f.Documents, f.DocumentMimetypes)
	}
	if !f.ForwardedManyTimes || !f.StatusMentions || f.StatusShares || f.GroupStatusPosts {
		t.Errorf("facts = %+v, want forwarded many times and a status mention only", f)
	}
	v := EvaluateContent(Defaults().Content, f, Standing{Role: RoleMember, Established: true})
	if got := rules(v); !slices.Equal(got, []string{"content.document_extensions_block", "content.document_extensions_block"}) {
		t.Errorf("default content policy on a renamed APK = %v, want it blocked by name and by type", got)
	}
}

func score(signals ...scoring.Signal) scoring.Result {
	return scoring.Score(scoring.Weights{Version: "test"}, signals)
}

func TestFindingFrom(t *testing.T) {
	known := scoring.Classify(score(
		scoring.Signal{Code: "url.blocklisted", Weight: 5, Known: true},
		scoring.Signal{Code: "intel.prefix", Weight: 0.4},
		scoring.Signal{Code: "content.videos", Weight: 0, Hard: true, Evidence: "video"},
	), scoring.DefaultThresholds())
	f := FindingFrom(known, TypeVideos, []Violation{{Rule: "content.videos", Detail: "video (block)"}}, t0)
	if f.Band != BandCertain || f.Source != SourceKnown || f.Type != TypeVideos || !f.At.Equal(t0) {
		t.Fatalf("finding = %+v, want certain known videos", f)
	}
	if len(f.Violations) != 1 {
		t.Errorf("violations = %v, want one per rule", f.Violations)
	}
	if len(f.Signals) != 2 || !f.Signals[0].Known || f.Signals[0].Prior || !f.Signals[1].Prior {
		t.Errorf("signals = %+v, want the known signal and the intel prior, hard signals excluded", f.Signals)
	}

	uncertain := scoring.Classify(score(scoring.Signal{Code: "kw.crypto", Weight: 3}), scoring.DefaultThresholds())
	verified := scoring.Resolve(uncertain, &ai.Verdict{Label: ai.LabelSpam, Confidence: 0.95}, scoring.DefaultThresholds())
	if f := FindingFrom(verified, TypeText, nil, t0); f.Band != BandUncertain || f.Source != SourceAI {
		t.Errorf("AI-verified finding = %s/%s, want uncertain/ai", f.Band, f.Source)
	}
	unsure := scoring.Resolve(uncertain, nil, scoring.DefaultThresholds())
	if d := Decide(Defaults(), FindingFrom(unsure, TypeText, nil, t0), Standing{Role: RoleMember}); d.Delete || d.Ban || d.Review == nil {
		t.Errorf("AI off: decision %+v, want admins asked and nothing deleted (§7.0)", d)
	}
	if f := FindingFrom(scoring.Verdict{}, TypeText, nil, t0); f.Band != BandClean {
		t.Errorf("empty verdict: band %q, want clean", f.Band)
	}
}

func TestJustJoinedScoringSignal(t *testing.T) {
	cfg := Defaults().NewMembers.JustJoined
	s, ok := JustJoinedScoringSignal(cfg, t0.Add(-6*time.Hour), t0)
	if !ok || s.Code != SignalJustJoined || s.Known || s.Hard || s.Weight < 0.59 || s.Weight > 0.61 {
		t.Fatalf("signal = %+v, %v; want a plain 0.6 just-joined signal", s, ok)
	}
	r := score(s)
	if r.LogOdds < 0.59 {
		t.Errorf("scored log-odds %v, want the group weight applied (no weight-table override)", r.LogOdds)
	}
	if _, ok := JustJoinedScoringSignal(cfg, time.Time{}, t0); ok {
		t.Error("pre-existing member (no join time): got a signal, want none")
	}
}

func TestEngineRoute(t *testing.T) {
	video := envelope.Envelope{Kind: envelope.KindVideo}
	knownSpam := scoring.Classify(score(scoring.Signal{Code: "media.sha256", Weight: 6, Known: true}), scoring.DefaultThresholds())
	clean := scoring.Classify(score(), scoring.DefaultThresholds())
	newbie := Standing{Role: RoleMember, New: true}

	noVideos := Defaults()
	noVideos.Content.Videos = RuleBlock
	shadow := Defaults()
	shadow.Mode = ModeShadow

	tests := []struct {
		name    string
		s       Settings
		v       scoring.Verdict
		route   Route
		enqueue []ActionKind
		mode    DetectionMode
	}{
		{"clean video", Defaults(), clean, RouteNone, nil, DetectionEnforce},
		{"video in a no-videos group: silent delete", noVideos, clean, RoutePolicy, []ActionKind{ActionDelete}, DetectionEnforce},
		{"known spam video: delete and ban", Defaults(), knownSpam, RouteAutoBan, []ActionKind{ActionDelete, ActionBan}, DetectionEnforce},
		{"known spam video, shadow: nothing enqueued", shadow, knownSpam, RouteAutoBan, nil, DetectionShadow},
	}
	for _, tt := range tests {
		e := NewEngine(clock.NewFake(t0))
		out := e.Route("g@g.us", tt.s, video, tt.v, newbie, t0)
		var kinds []ActionKind
		for _, a := range out.Plan.Enqueue {
			kinds = append(kinds, a.Kind)
		}
		if out.Decision.Route != tt.route || !slices.Equal(kinds, tt.enqueue) || out.Plan.Mode != tt.mode {
			t.Errorf("%s: route %s, enqueue %v, mode %q; want %s, %v, %q", tt.name, out.Decision.Route, kinds, out.Plan.Mode, tt.route, tt.enqueue, tt.mode)
		}
	}

	e := NewEngine(clock.NewFake(t0))
	e.Rails().KillRule("content.videos")
	if out := e.Route("g@g.us", noVideos, video, clean, newbie, t0); out.Decision.Route != RouteNone || len(out.Plan.Enqueue) != 0 {
		t.Errorf("killed content rule: %+v, want nothing", out.Decision)
	}
}
