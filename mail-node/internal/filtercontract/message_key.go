package filtercontract

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

func MessageKey(serverID uint64, mailbox, maildirUniqueName string, sizeBytes int64) string {
	uniqueName := maildirUniqueName
	if index := strings.Index(uniqueName, ":2,"); index >= 0 {
		uniqueName = uniqueName[:index]
	}
	input := strconv.FormatUint(serverID, 10) + "\x00" + mailbox + "\x00" + uniqueName + "\x00" + strconv.FormatInt(sizeBytes, 10)
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}
