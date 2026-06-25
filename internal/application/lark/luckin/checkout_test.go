package luckin

import "testing"

func TestSelectCheckoutItemsByMode(t *testing.T) {
	cart := Cart{Items: []CartItem{
		{LineID: "1", ProductName: "A", Amount: 2, AddedByOpenID: "ou_a"},
		{LineID: "2", ProductName: "B", Amount: 1, AddedByOpenID: "ou_b"},
	}}

	all := SelectCheckoutItems(cart, CheckoutModeInitiatorUnified, "ou_a")
	if len(all) != 2 {
		t.Fatalf("unified items = %d, want 2", len(all))
	}

	self := SelectCheckoutItems(cart, CheckoutModeSelfService, "ou_b")
	if len(self) != 1 || self[0].LineID != "2" {
		t.Fatalf("self items = %+v", self)
	}
}

func TestRemoveCheckoutItemsByMode(t *testing.T) {
	cart := Cart{Items: []CartItem{
		{LineID: "1", ProductName: "A", Amount: 2, AddedByOpenID: "ou_a"},
		{LineID: "2", ProductName: "B", Amount: 1, AddedByOpenID: "ou_b"},
	}}

	remaining := RemoveCheckoutItems(cart, CheckoutModeSelfService, "ou_a")
	if len(remaining.Items) != 1 || remaining.Items[0].LineID != "2" {
		t.Fatalf("remaining = %+v", remaining.Items)
	}

	cleared := RemoveCheckoutItems(cart, CheckoutModeInitiatorUnified, "ou_a")
	if len(cleared.Items) != 0 {
		t.Fatalf("cleared items = %+v", cleared.Items)
	}
}

func TestSplitItemsToSingleCupOrders(t *testing.T) {
	orders := SplitItemsToSingleCupOrders([]CartItem{
		{LineID: "1", ProductName: "A", Amount: 2, AddedByOpenID: "ou_a"},
		{LineID: "2", ProductName: "B", Amount: 1, AddedByOpenID: "ou_b"},
	})
	if len(orders) != 3 {
		t.Fatalf("orders = %d, want 3", len(orders))
	}
	for i, order := range orders {
		if len(order) != 1 || order[0].Amount != 1 {
			t.Fatalf("order[%d] = %+v", i, order)
		}
	}
}
