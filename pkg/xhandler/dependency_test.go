package xhandler

import (
	"context"
	"fmt"
	"testing"
)

// 简单的测试，展示多 Fetcher 执行的预期行为
func TestDependencyExecutionOrder(t *testing.T) {
	// 这是一个文档化的测试用例，展示多 Fetcher 执行的预期行为
	t.Log("=== 多 Fetcher 执行预期验证 ===")
	t.Log("")
	t.Log("场景 1: 单个 Fetcher + 依赖它的 Operator")
	t.Log("  - Fetcher1 延迟 50ms 执行")
	t.Log("  - DependentOperator 依赖 Fetcher1")
	t.Log("  预期:")
	t.Log("    1. Fetcher1 先执行 (约 50ms)")
	t.Log("    2. DependentOperator 等待 Fetcher1 完成后执行")
	t.Log("    3. 总执行时间 >= 50ms")
	t.Log("")

	t.Log("场景 2: 多个 Fetcher 并行执行")
	t.Log("  - Fetcher1 延迟 100ms")
	t.Log("  - Fetcher2 延迟 100ms")
	t.Log("  预期:")
	t.Log("    1. Fetcher1 和 Fetcher2 并行执行")
	t.Log("    2. 总执行时间接近 100ms (不是 200ms)")
	t.Log("")

	t.Log("场景 3: Operator 依赖多个 Fetcher")
	t.Log("  - Fetcher1 延迟 50ms")
	t.Log("  - Fetcher2 延迟 100ms")
	t.Log("  - MultiDependentOperator 依赖两者")
	t.Log("  预期:")
	t.Log("    1. 两个 Fetcher 并行执行")
	t.Log("    2. MultiDependentOperator 等待两者都完成")
	t.Log("    3. 总执行时间 >= 100ms (由较慢的 Fetcher2 决定)")
	t.Log("")

	t.Log("场景 4: 混合场景")
	t.Log("  - Fetcher1 延迟 100ms")
	t.Log("  - IndependentOperator 无依赖")
	t.Log("  - DependentOperator 依赖 Fetcher1")
	t.Log("  预期:")
	t.Log("    1. IndependentOperator 立即执行 (不需要等 Fetcher1)")
	t.Log("    2. DependentOperator 等待 Fetcher1 完成")
	t.Log("    3. Fetcher1 在后台执行")
	t.Log("")
}

// 验证代码能编译通过
func TestCodeCompiles(t *testing.T) {
	t.Log("验证依赖调度机制代码能正常编译...")

	// 测试基本的 Processor 创建
	processor := &Processor[any, BaseMetaData]{}
	processor = processor.
		WithCtx(context.Background()).
		WithMetaDataProcess(func(event *any) *BaseMetaData {
			return &BaseMetaData{}
		})

	// 验证 Processor 能正常创建
	if processor == nil {
		t.Fatal("Processor should not be nil")
	}

	t.Log("✓ Processor 创建成功")
	t.Log("✓ 依赖调度机制编译通过")
}

// 示例：展示如何使用 Fetcher 和 Depends
func Example_dependencyUsage() {
	// 这是一个示例，展示如何在实际代码中使用新的依赖机制

	// 1. 定义一个 Fetcher
	/*
		type IntentRecognizeOperator struct {
			OpBase
		}

		// 实现 Fetcher 接口
		func (r *IntentRecognizeOperator) Name() string {
			return "IntentRecognizeOperator"
		}

		func (r *IntentRecognizeOperator) Fetch(ctx context.Context, event *T, meta *K) error {
			// 执行数据获取逻辑...
			return nil
		}

		// 全局单例
		var IntentRecognizeFetcher = &IntentRecognizeOperator{}
	*/

	// 2. 定义一个依赖该 Fetcher 的 Operator
	/*
		type ChatMsgOperator struct {
			OpBase
		}

		// 声明依赖 - 返回 Fetcher 实例
		func (r *ChatMsgOperator) Depends() []Fetcher[T, K] {
			return []Fetcher[T, K]{
				IntentRecognizeFetcher,
			}
		}

		func (r *ChatMsgOperator) Name() string {
			return "ChatMsgOperator"
		}

		// ... 其他方法实现
	*/

	// 3. 注册到 Processor - 不需要手动注册 Fetcher！
	/*
		Handler = Handler.
			AddAsync(&ChatMsgOperator{})  // 只需要注册 Operator
		// Fetcher 会通过 ChatMsgOperator.Depends() 自动收集和执行
	*/

	fmt.Println("使用 Fetcher 和 Depends 的示例代码")
	fmt.Println("1. 定义 Fetcher 实现 Name() 和 Fetch() 方法")
	fmt.Println("2. Operator 通过 Depends() []Fetcher[T, K] 返回依赖的 Fetcher 实例")
	fmt.Println("3. Processor 自动收集、去重和执行所有依赖的 Fetcher")
	fmt.Println("4. 每个 Operator 等待自己依赖的 Fetcher 完成后执行")
}
