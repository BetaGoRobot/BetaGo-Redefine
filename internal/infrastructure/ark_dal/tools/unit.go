package tools

type FunctionCallUnit[T any] struct {
	FunctionName string
	Description  string
	Parameters   *Param
	Function     HandlerFunc[T]
}

func (h *FunctionCallUnit[T]) Name(name string) *FunctionCallUnit[T] {
	h.FunctionName = name
	return h
}

func (h *FunctionCallUnit[T]) Desc(desc string) *FunctionCallUnit[T] {
	h.Description = desc
	return h
}

func (h *FunctionCallUnit[T]) Params(params *Param) *FunctionCallUnit[T] {
	h.Parameters = params
	return h
}

func (h *FunctionCallUnit[T]) Func(f HandlerFunc[T]) *FunctionCallUnit[T] {
	h.Function = f
	return h
}

func NewUnit[T any]() *FunctionCallUnit[T] {
	return &FunctionCallUnit[T]{}
}
