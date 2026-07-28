package db

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

func TestTransactionContextRoundTripAndTypedNilSafety(t *testing.T) {
	tx := &gorm.DB{}
	var borrowed context.Context
	if err := WithTransactionContext(context.Background(), tx, func(ctx context.Context) error {
		borrowed = ctx
		got, ok := TransactionFromContext(ctx)
		if !ok || got != tx {
			t.Fatalf("TransactionFromContext() = %#v, %v; want original tx", got, ok)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTransactionContext() error = %v", err)
	}
	if got, ok := TransactionFromContext(borrowed); ok || got != nil {
		t.Fatalf("TransactionFromContext(after scope) = %#v, %v; want nil,false", got, ok)
	}

	var typedNil *gorm.DB
	called := false
	if err := WithTransactionContext(context.Background(), typedNil, func(context.Context) error {
		called = true
		return nil
	}); err == nil {
		t.Fatal("WithTransactionContext(typed nil) error = nil")
	}
	if called {
		t.Fatal("WithTransactionContext(typed nil) invoked callback")
	}
	if got, ok := TransactionFromContext(nil); ok || got != nil {
		t.Fatalf("TransactionFromContext(nil ctx) = %#v, %v; want nil,false", got, ok)
	}
}
