package adapter

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// maxReflectDepth bounds how deep reflection descends into nested messages.
const maxReflectDepth = 10

// reflect reads every string under v (DESIGN.md §6.13): URLs go to the URL list and other text
// to text surfaces, both under source. Technical fields (IDs, hashes, paths, mimetypes, media
// download URLs) are skipped, embedded media become MediaRefs, and ContextInfo is left to
// context().
func (b *builder) reflect(v protoreflect.Value, source string, depth int) {
	if depth > maxReflectDepth {
		return
	}
	switch x := v.Interface().(type) {
	case string:
		b.reflectString(source, x)
	case protoreflect.Message:
		b.reflectMessage(x, source, depth)
	case protoreflect.List:
		for i := range x.Len() {
			b.reflect(x.Get(i), source, depth+1)
		}
	case protoreflect.Map:
		for _, k := range sortedKeys(x) {
			b.reflect(x.Get(k), source, depth+1)
		}
	}
}

// sortedKeys returns a map field's keys in a fixed order. Map.Range has none, and the text
// order feeds normalization, fingerprints and hashes, which must not change between runs.
func sortedKeys(m protoreflect.Map) []protoreflect.MapKey {
	keys := make([]protoreflect.MapKey, 0, m.Len())
	m.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, k)
		return true
	})
	slices.SortFunc(keys, func(a, b protoreflect.MapKey) int {
		return cmp.Compare(fmt.Sprint(a.Interface()), fmt.Sprint(b.Interface()))
	})
	return keys
}

func (b *builder) reflectMessage(m protoreflect.Message, source string, depth int) {
	switch im := m.Interface().(type) {
	case *waE2E.ImageMessage:
		b.media(envelope.KindImage, im, im.GetJPEGThumbnail())
	case *waE2E.VideoMessage:
		b.media(envelope.KindVideo, im, im.GetJPEGThumbnail())
	case *waE2E.DocumentMessage:
		b.media(envelope.KindDocument, im, im.GetJPEGThumbnail())
	case *waE2E.ContextInfo:
		return
	}
	fields := m.Descriptor().Fields()
	isMedia := fields.ByName("fileSHA256") != nil
	// Fields go in declaration order. Message.Range is deliberately randomized per binary by
	// protobuf-go, and the text order feeds normalization, fingerprints and hashes.
	for i := range fields.Len() {
		fd := fields.Get(i)
		if !m.Has(fd) || skipField(fd, isMedia) {
			continue
		}
		b.reflect(m.Get(fd), source, depth+1)
	}
}

func (b *builder) reflectString(source, s string) {
	if looksLikeURL(s) {
		b.url(source, s)
		return
	}
	b.text(source, s)
}

// skipField reports whether a string field is technical rather than content.
func skipField(fd protoreflect.FieldDescriptor, inMedia bool) bool {
	if fd.Kind() == protoreflect.BytesKind || fd.Kind() == protoreflect.EnumKind {
		return true
	}
	n := strings.ToLower(string(fd.Name()))
	if inMedia && n == "url" {
		return true // the encrypted media download URL
	}
	switch n {
	case "mimetype", "directpath", "thumbnaildirectpath", "staticurl", "metadataurl", "dashmanifesturl",
		"phash", "token", "handle", "encpayload", "enciv":
		return true
	}
	for _, suffix := range []string{"id", "jid", "hash", "sha256", "key", "path", "timestamp", "ts"} {
		if strings.HasSuffix(n, suffix) && fd.Kind() != protoreflect.MessageKind {
			return true
		}
	}
	return false
}

// looksLikeURL reports whether s is a single URL rather than text containing one. Text with
// URLs inside is left to the URL extractor, which runs on every text surface.
func looksLikeURL(s string) bool {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, " \n\t") {
		return false
	}
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}
