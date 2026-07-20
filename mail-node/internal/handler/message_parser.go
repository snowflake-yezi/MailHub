package handler

import (
	"os"

	"github.com/jhillyerd/enmime"
	"github.com/ticket/email-mail-node/internal/mailparse"
)

type parsedAttachment = mailparse.ParsedAttachment
type parsedMessage = mailparse.ParsedMessage
type inferredPartInfo = mailparse.PartInfo

func parseMaildirMessage(filePath, mailbox, maildirBase string) (*parsedMessage, error) {
	return mailparse.ParseSummary(filePath, mailbox, maildirBase)
}

func parseFullMessage(filePath, mailbox, maildirBase string) (*parsedMessage, error) {
	return mailparse.ParseFull(filePath, mailbox, maildirBase)
}

func fallbackMessageID(filePath, maildirBase string, stat os.FileInfo) string {
	return mailparse.FallbackMessageID(filePath, maildirBase, stat)
}

func collectAttachmentParts(envelope *enmime.Envelope) []*enmime.Part {
	return mailparse.AttachmentParts(envelope)
}

func collectAttachments(envelope *enmime.Envelope) []parsedAttachment {
	return mailparse.Attachments(envelope)
}

func isInlinePart(part *enmime.Part, inlineContentIDs map[string]struct{}) bool {
	return mailparse.IsInlinePart(part, inlineContentIDs)
}

func htmlCIDReferences(htmlBody string) map[string]struct{} {
	return mailparse.HTMLCIDReferences(htmlBody)
}

func inferPartInfo(part *enmime.Part, index int, inline bool) inferredPartInfo {
	return mailparse.InferPartInfo(part, index, inline)
}

func htmlToPlainText(input string) string {
	return mailparse.HTMLToPlainText(input)
}
