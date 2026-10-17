package policy

import (
	"slices"
	"testing"
)

func TestContentHostsAndMimetypes(t *testing.T) {
	def := Defaults().Content
	blockBitly := Defaults().Clone().Content
	blockBitly.LinkDomains.Block = []string{"bit.ly"}
	blockIDN := Defaults().Clone().Content
	blockIDN.LinkDomains.Block = []string{"xn--bcher-kva.example"}
	est := Standing{Role: RoleMember, Established: true}
	tests := []struct {
		name string
		c    Content
		f    ContentFacts
		want []string
	}{
		{"port and fullwidth dot", blockBitly, ContentFacts{LinkHosts: []string{"BIT．LY:443"}}, []string{"content.link_domains.block"}},
		{"ideographic dot", blockBitly, ContentFacts{LinkHosts: []string{"bit。ly"}}, []string{"content.link_domains.block"}},
		{"subdomain", blockBitly, ContentFacts{LinkHosts: []string{"go.bit.ly"}}, []string{"content.link_domains.block"}},
		{"look-alike suffix is not the domain", blockBitly, ContentFacts{LinkHosts: []string{"notbit.ly"}}, nil},
		{"internationalised name matches its punycode", blockIDN, ContentFacts{LinkHosts: []string{"bücher.example"}}, []string{"content.link_domains.block"}},
		{"apk renamed to pdf", def, ContentFacts{Documents: []string{"invoice.pdf"}, DocumentMimetypes: []string{"application/vnd.android.package-archive"}}, []string{"content.document_extensions_block"}},
		{"mimetype with parameters", def, ContentFacts{HasDocument: true, DocumentMimetypes: []string{"Application/X-MSDownload; charset=binary"}}, []string{"content.document_extensions_block"}},
		{"pdf", def, ContentFacts{Documents: []string{"invoice.pdf"}, DocumentMimetypes: []string{"application/pdf"}}, nil},
		{"executable type not on the block list", def, ContentFacts{HasDocument: true, DocumentMimetypes: []string{"application/java-archive"}}, nil},
	}
	for _, tt := range tests {
		if got := rules(EvaluateContent(tt.c, tt.f, est)); !slices.Equal(got, tt.want) {
			t.Errorf("%s: violations %v, want %v (§9.3)", tt.name, got, tt.want)
		}
	}
}
