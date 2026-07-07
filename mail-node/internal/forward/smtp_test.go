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
