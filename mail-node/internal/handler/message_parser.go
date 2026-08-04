package handler

import (
	"os"

	"github.com/ticket/email-mail-node/internal/mailparse"
)

type parsedAttachment = mailparse.ParsedAttachment
type parsedMessage = mailparse.ParsedMessage
type inferredPartInfo = mailparse.PartInfo
type parsedPart = mailparse.ParsedPart

func parseMaildirMessage(filePath, mailbox, maildirBase string) (*parsedMessage, error) {
	return mailparse.ParseSummary(filePath, mailbox, maildirBase)
}

func parseFullMessage(filePath, mailbox, maildirBase string) (*parsedMessage, error) {
	return mailparse.ParseFull(filePath, mailbox, maildirBase)
}

func fallbackMessageID(filePath, maildirBase string, stat os.FileInfo) string {
	return mailparse.FallbackMessageID(filePath, maildirBase, stat)
}

func htmlToPlainText(input string) string {
	return mailparse.HTMLToPlainText(input)
}
