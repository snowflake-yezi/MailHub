package forward

import (
	"bytes"
	"strings"
	"testing"
)

func TestNormalizeForwardedInlineImagePartsRepairsCIDReferencedJPEG(t *testing.T) {
	body := []byte(strings.ReplaceAll(`--related-boundary
Content-Type: text/html; charset="utf-8"

<div><img src="cid:283F2ACD@3D967052.E5884B6A00000000"></div>
--related-boundary
Content-Type: application/octet-stream
Content-ID: <283F2ACD@3D967052.E5884B6A00000000>
Content-Transfer-Encoding: base64

/9j/4AAQSkZJRgAB
--related-boundary--
`, "\n", "\r\n"))

	got := normalizeForwardedInlineImageParts(body)
	gotText := string(got)
	if !strings.Contains(gotText, `Content-Type: image/jpeg; name="283F2ACD@3D967052.E5884B6A00000000.jpg"`) {
		t.Fatalf("normalized body missing image/jpeg name header:\n%s", gotText)
	}
	if !strings.Contains(gotText, `Content-Disposition: inline; filename="283F2ACD@3D967052.E5884B6A00000000.jpg"`) {
		t.Fatalf("normalized body missing inline filename header:\n%s", gotText)
	}
	if !bytes.Contains(got, []byte("/9j/4AAQSkZJRgAB")) {
		t.Fatalf("normalized body changed encoded image payload:\n%s", gotText)
	}
}

func TestNormalizeForwardedInlineImagePartsLeavesUnreferencedAttachmentAlone(t *testing.T) {
	body := []byte(strings.ReplaceAll(`--mixed-boundary
Content-Type: text/html; charset="utf-8"

<div>hello</div>
--mixed-boundary
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="photo"
Content-Transfer-Encoding: base64

/9j/4AAQSkZJRgAB
--mixed-boundary--
`, "\n", "\r\n"))

	got := normalizeForwardedInlineImageParts(body)
	if !bytes.Equal(got, body) {
		t.Fatalf("unreferenced attachment changed:\n%s", string(got))
	}
}

func TestNormalizeForwardedInlineImagePartsNestedMultipartAndFoldedHeaders(t *testing.T) {
	body := []byte(strings.ReplaceAll(`preamble
--outer
Content-Type: multipart/related; boundary="inner"

--inner
Content-Type: text/html; charset="utf-8"

<html><img src="cid:logo@example.com"></html>
--inner
Content-Type: application/octet-stream
Content-ID: <logo@example.com>
Content-Disposition: inline;
 filename="logo"
Content-Transfer-Encoding: base64

iVBORw0KGgo=
--inner--
--outer--
epilogue
`, "\n", "\r\n"))
	got := normalizeForwardedInlineImagePartsWithContentType(body, `multipart/mixed; boundary="outer"`)
	text := string(got)
	if !strings.Contains(text, `Content-Type: image/png; name="logo.png"`) {
		t.Fatalf("nested inline part was not repaired:\n%s", text)
	}
	if !strings.Contains(text, `Content-Disposition: inline; filename="logo.png"`) {
		t.Fatalf("nested disposition was not repaired:\n%s", text)
	}
	if !strings.HasPrefix(text, "preamble\r\n") || !strings.HasSuffix(text, "epilogue\r\n") {
		t.Fatalf("preamble/epilogue were not preserved:\n%s", text)
	}
	if strings.Count(text, "epilogue") != 1 {
		t.Fatalf("epilogue was duplicated: %q", text)
	}
	if !bytes.Contains(got, []byte("iVBORw0KGgo=")) {
		t.Fatalf("encoded payload disappeared")
	}
	missingType := bytes.Replace(body, []byte("Content-Type: application/octet-stream\r\n"), nil, 1)
	missingType = normalizeForwardedInlineImagePartsWithContentType(missingType, `multipart/mixed; boundary="outer"`)
	if !bytes.Contains(missingType, []byte(`Content-Type: image/png; name="logo.png"`)) {
		t.Fatalf("missing declared content type was not repaired")
	}
}
