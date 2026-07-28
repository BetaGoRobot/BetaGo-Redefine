package db

import (
	"context"
	"errors"
	"sync/atomic"

	"gorm.io/gorm"
)

var errInvalidTransactionContext = errors.New("invalid transaction context")

type transactionContextKey struct{}

type transactionBinding struct {
	tx     *gorm.DB
	active atomic.Bool
}

// WithTransactionContext lends tx to callback through its context. The bound
// context is valid only for the synchronous lifetime of callback and must not
// be retained or used by asynchronous work.
func WithTransactionContext(
	ctx context.Context,
	tx *gorm.DB,
	callback func(context.Context) error,
) error {
	if tx == nil || tx.Error != nil || callback == nil {
		return errInvalidTransactionContext
	}
	if ctx == nil {
		ctx = context.Background()
	}
	binding := &transactionBinding{tx: tx}
	binding.active.Store(true)
	defer binding.active.Store(false)
	return callback(context.WithValue(ctx, transactionContextKey{}, binding))
}

// TransactionFromContext returns a transaction only while its lending
// callback is active. Expired and malformed bindings fail closed.
func TransactionFromContext(ctx context.Context) (*gorm.DB, bool) {
	if ctx == nil {
		return nil, false
	}
	binding, ok := ctx.Value(transactionContextKey{}).(*transactionBinding)
	if !ok || binding == nil || !binding.active.Load() ||
		binding.tx == nil || binding.tx.Error != nil {
		return nil, false
	}
	return binding.tx, true
}
