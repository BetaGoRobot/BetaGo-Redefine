package luckin

import (
	"github.com/google/uuid"
)

// CartItem 购物车单条目。
// 同一发起人的同 productID+skuCode 视为同一条目并累加数量；不同人加入的同 SKU 各占一行，
// 便于"取餐时按人分账"。LineID 是行的稳定唯一标识，按钮 payload 用它定位条目，
// 避免不同人 +/- 同 SKU 时互相干扰。
type CartItem struct {
	LineID        string
	ProductID     int64
	SkuCode       string
	ProductName   string
	Amount        int
	UnitPrice     float64
	ImageKey      string
	AddedByOpenID string
}

const (
	cartMinAmount = 1
	cartMaxAmount = 20
)

// Cart 一次点单流程内的共享购物车，跨群成员共用。
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

// Add 加入商品。同一发起人 + 同 productID+skuCode 累加数量；其他用户加入同 SKU 单起一行。
// 不带 LineID 调用 Add 时会自动生成；调用方也可预设 LineID 以保证幂等。
func (c *Cart) Add(item CartItem) {
	item.Amount = ClampAmount(item.Amount)
	for i := range c.Items {
		if c.Items[i].ProductID != item.ProductID || c.Items[i].SkuCode != item.SkuCode {
			continue
		}
		if c.Items[i].AddedByOpenID != item.AddedByOpenID {
			continue
		}
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
	if item.LineID == "" {
		item.LineID = uuid.NewString()
	}
	c.Items = append(c.Items, item)
}

// SetAmountByLine 按 LineID 设置某条目数量；数量<=0 视为删除。返回是否命中条目。
func (c *Cart) SetAmountByLine(lineID string, amount int) bool {
	for i := range c.Items {
		if c.Items[i].LineID != lineID {
			continue
		}
		if amount <= 0 {
			c.removeAt(i)
			return true
		}
		c.Items[i].Amount = ClampAmount(amount)
		return true
	}
	return false
}

// RemoveByLine 按 LineID 删除某条目，返回是否命中。
func (c *Cart) RemoveByLine(lineID string) bool {
	for i := range c.Items {
		if c.Items[i].LineID == lineID {
			c.removeAt(i)
			return true
		}
	}
	return false
}

// FindLine 按 LineID 找到条目（只读），用于权限校验。
func (c *Cart) FindLine(lineID string) (CartItem, bool) {
	for _, item := range c.Items {
		if item.LineID == lineID {
			return item, true
		}
	}
	return CartItem{}, false
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
