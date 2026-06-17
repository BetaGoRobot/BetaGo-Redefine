package luckin

// CartItem 购物车单条目，同 productID+skuCode 视为同一条目并累加数量。
type CartItem struct {
	ProductID   int64
	SkuCode     string
	ProductName string
	Amount      int
	UnitPrice   float64
	ImageKey    string
}

const (
	cartMinAmount = 1
	cartMaxAmount = 20
)

// Cart 会话级购物车。
type Cart struct {
	Items []CartItem
}

// ClampAmount 把数量钳制到合理区间。
func ClampAmount(amount int) int {
	if amount < cartMinAmount {
		return cartMinAmount
	}
	if amount > cartMaxAmount {
		return cartMaxAmount
	}
	return amount
}

// Add 加入商品；同 productID+skuCode 累加数量（不超过上限）。
func (c *Cart) Add(item CartItem) {
	item.Amount = ClampAmount(item.Amount)
	for i := range c.Items {
		if c.Items[i].ProductID == item.ProductID && c.Items[i].SkuCode == item.SkuCode {
			c.Items[i].Amount = ClampAmount(c.Items[i].Amount + item.Amount)
			if item.UnitPrice > 0 {
				c.Items[i].UnitPrice = item.UnitPrice
			}
				if item.ProductName != "" {
					c.Items[i].ProductName = item.ProductName
				}
				if item.ImageKey != "" {
					c.Items[i].ImageKey = item.ImageKey
				}
				return
			}
	}
	c.Items = append(c.Items, item)
}

// SetAmount 设置某条目数量；数量<=0 视为删除。返回是否命中条目。
func (c *Cart) SetAmount(productID int64, skuCode string, amount int) bool {
	for i := range c.Items {
		if c.Items[i].ProductID == productID && c.Items[i].SkuCode == skuCode {
			if amount <= 0 {
				c.removeAt(i)
				return true
			}
			c.Items[i].Amount = ClampAmount(amount)
			return true
		}
	}
	return false
}

// Remove 删除某条目，返回是否命中。
func (c *Cart) Remove(productID int64, skuCode string) bool {
	for i := range c.Items {
		if c.Items[i].ProductID == productID && c.Items[i].SkuCode == skuCode {
			c.removeAt(i)
			return true
		}
	}
	return false
}

func (c *Cart) removeAt(i int) {
	c.Items = append(c.Items[:i], c.Items[i+1:]...)
}

// Empty 购物车是否为空。
func (c *Cart) Empty() bool {
	return c == nil || len(c.Items) == 0
}

// TotalAmount 全部条目数量之和。
func (c *Cart) TotalAmount() int {
	total := 0
	for _, item := range c.Items {
		total += item.Amount
	}
	return total
}

// EstimatedTotal 预估总价（单价 * 数量之和），仅用于展示，下单以 preview 为准。
func (c *Cart) EstimatedTotal() float64 {
	total := 0.0
	for _, item := range c.Items {
		total += item.UnitPrice * float64(item.Amount)
	}
	return total
}
