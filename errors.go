package unitdb

import (
	"errors"
)

var (
	errTopicEmpty          = errors.New("Topic is empty")
	errMsgIdEmpty          = errors.New("Message Id is empty")
	errMsgIdDeleted        = errors.New("Message Id is deleted")
	errMsgIdDoesNotExist   = errors.New("Message Id does not exist in database")
	errMsgIdPrefixMismatch = errors.New("Message Id does not match topic or Contract")
	errTtlTooLarge         = errors.New("TTL is too large")
	errTopicTooLarge       = errors.New("Topic is too large")
	errMsgExpired          = errors.New("Message has expired")
	errValueEmpty          = errors.New("Payload is empty")
	errValueTooLarge       = errors.New("value is too large")
	errEntryInvalid        = errors.New("entry is invalid")
	errImmutable           = errors.New("database is immutable")
	errFull                = errors.New("database is full")
	errCorrupted           = errors.New("database is corrupted")
	errLocked              = errors.New("database is locked")
	errClosed              = errors.New("database is closed")
	errBatchSeqComplete    = errors.New("batch seq is complete")
	errWriteConflict       = errors.New("batch write conflict")
	errBadRequest          = errors.New("The request was invalid or cannot be otherwise served")
	errForbidden           = errors.New("The request is understood, but it has been refused or access is not allowed")
)
