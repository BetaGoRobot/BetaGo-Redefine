package luckin

import "strings"

// SelectCheckoutItems returns the cart items included in the current checkout.
func SelectCheckoutItems(cart Cart, mode CheckoutMode, operatorOpenID string) []CartItem {
	mode = NormalizeCheckoutMode(string(mode))
	operatorOpenID = strings.TrimSpace(operatorOpenID)
	if mode == CheckoutModeInitiatorUnified {
		out := make([]CartItem, 0, len(cart.Items))
		out = append(out, cart.Items...)
		return out
	}
	if operatorOpenID == "" {
		return nil
	}
	out := make([]CartItem, 0, len(cart.Items))
	for _, item := range cart.Items {
		if strings.TrimSpace(item.AddedByOpenID) == operatorOpenID {
			out = append(out, item)
		}
	}
	return out
}

// RemoveCheckoutItems removes the items covered by the current checkout from the cart.
func RemoveCheckoutItems(cart Cart, mode CheckoutMode, operatorOpenID string) Cart {
	mode = NormalizeCheckoutMode(string(mode))
	operatorOpenID = strings.TrimSpace(operatorOpenID)
	if mode == CheckoutModeInitiatorUnified {
		return Cart{}
	}
	if operatorOpenID == "" {
		return cart
	}
	remaining := make([]CartItem, 0, len(cart.Items))
	for _, item := range cart.Items {
		if strings.TrimSpace(item.AddedByOpenID) == operatorOpenID {
			continue
		}
		remaining = append(remaining, item)
	}
	return Cart{Items: remaining}
}

// SplitItemsToSingleCupOrders expands quantities so each resulting order contains exactly one cup.
func SplitItemsToSingleCupOrders(items []CartItem) [][]CartItem {
	out := make([][]CartItem, 0, len(items))
	for _, item := range items {
		amount := ClampAmount(item.Amount)
		for i := 0; i < amount; i++ {
			single := item
			single.Amount = 1
			out = append(out, []CartItem{single})
		}
	}
	return out
}
