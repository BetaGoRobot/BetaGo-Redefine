# WebUI Visual System Modernization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the approved warm Agent operations design across the historical WebUI without changing API or analytics behavior.

**Architecture:** A global CSS token layer and a single application shell
provide the visual foundation. Existing view-model logic remains in place;
templates gain semantic layout classes, shared responsive rules, and explicit
empty states. Source-level Vitest assertions protect structural invariants,
while browser screenshots verify actual desktop and mobile rendering.

**Tech Stack:** Vue 3.5, TypeScript 5.7, Element Plus 2.9, ECharts 6, Pinia,
Vitest, Vite, Agent Browser.

---

### Task 1: Protect the visual-system structure with failing tests

**Files:**
- Create: `webui/src/visual-system.test.ts`

- [ ] **Step 1: Write source-level structure tests**

Add assertions that:

```ts
expect(main).toContain("import './styles/theme.css'")
expect(app).toContain('class="app-shell"')
expect(app).toContain('class="app-nav"')
expect(filter).toContain('class="filter-dock"')
expect(filter).not.toContain('总览仪表盘')
expect(dashboard).toContain('class="dashboard-empty"')
expect(chatList).toContain('class="mobile-chat-list"')
```

- [ ] **Step 2: Verify RED**

Run:

```bash
cd webui
npm test -- src/visual-system.test.ts
```

Expected: FAIL because the global theme, shell, filter dock, Dashboard empty
state, and mobile chat list do not exist.

### Task 2: Add the global theme and application shell

**Files:**
- Create: `webui/src/styles/theme.css`
- Modify: `webui/src/main.ts`
- Modify: `webui/src/App.vue`

- [ ] **Step 1: Define shared tokens and Element Plus overrides**

Create pine, canvas, surface, border, muted, lime, teal, amber, danger,
shadow, radius, and typography variables under `:root`. Style `body`,
`#app`, focus-visible, cards, buttons, inputs, dialogs, tables, loading
states, scrollbars, reduced motion, and the 768/1024 px breakpoints.

- [ ] **Step 2: Import the global theme**

Import `./styles/theme.css` after Element Plus CSS in `main.ts`.

- [ ] **Step 3: Replace the inline application header**

Build `.app-shell`, `.app-header`, `.app-brand`, `.app-nav`,
`.app-runtime-status`, and `.app-workspace`. Use text plus inline SVG icons;
remove emoji labels and inline style attributes.

- [ ] **Step 4: Verify GREEN for shell assertions**

Run:

```bash
cd webui
npm test -- src/visual-system.test.ts
```

Expected: shell and theme assertions pass while later Dashboard/chat
assertions remain red.

### Task 3: Modernize shared filters, Bot picker, and Dashboard

**Files:**
- Modify: `webui/src/components/GlobalFilterBar.vue`
- Modify: `webui/src/components/BotPicker.vue`
- Modify: `webui/src/views/Dashboard.vue`
- Modify: `webui/src/composables/useChartOptions.ts`

- [ ] **Step 1: Turn GlobalFilterBar into a filter dock**

Remove its route radio buttons. Render four `.filter-dock__group` sections
for Bot source, window, primary metric, and secondary metric. Preserve the
existing store method bindings and drill breadcrumb behavior.

- [ ] **Step 2: Restyle BotPicker**

Replace emoji action copy, inline dropdown layout, and fixed dialog width
with semantic classes and `width="min(32rem, calc(100vw - 2rem))"`.

- [ ] **Step 3: Add Dashboard header and empty state**

Render `.page-intro`, then `GlobalFilterBar`. If no selected bots exist,
render `.dashboard-empty`; otherwise render the KPI and chart layout.

- [ ] **Step 4: Replace fixed Dashboard columns**

Use `.metric-grid`, `.chart-grid--four`, `.chart-grid--primary`, and
`.chart-grid--split` CSS grids around existing cards. Keep every chart
option and click handler unchanged.

- [ ] **Step 5: Align ECharts palette**

Set the palette to pine, teal, lime-green, amber, coral, sky, and slate;
change axis, grid, tooltip, and title neutrals to the shared visual system.

- [ ] **Step 6: Run focused and full frontend tests**

Run:

```bash
cd webui
npm test -- src/visual-system.test.ts
npm test
```

Expected: all assertions through Dashboard pass and existing behavior remains
green.

### Task 4: Modernize historical chat surfaces

**Files:**
- Modify: `webui/src/views/ChatList.vue`
- Modify: `webui/src/views/ChatDetail.vue`

- [ ] **Step 1: Replace ChatList inline filter layout**

Add `.chat-filter-layout`, `.chat-filter-controls`,
`.chat-filter-control`, `.chat-distributions`, `.active-filters`, and
`.quick-filters`. Preserve every filter model and reset action.

- [ ] **Step 2: Add the mobile chat-card view**

Below the desktop table, render `.mobile-chat-list` cards from `filtered`.
Each card includes bot identity, chat avatar/name/id, Agentic rollout summary,
membership/type labels, recent messages, tokens, and the existing `open(row)`
action. CSS shows the table at 768 px and above, cards below 768 px.

- [ ] **Step 3: Align ChatDetail identity and tabs**

Replace header inline layout with `.chat-detail-identity`; remove emoji from
tab labels; add semantic control-row and drill-banner classes.

- [ ] **Step 4: Make historical detail grids responsive**

At 1023 px override quarter-width columns to half-width and 16/8 analytical
rows to full-width where necessary. At 767 px make all statistic and chart
columns full-width, maintain scrollable tabs/config tables, and enforce
touch-size controls.

- [ ] **Step 5: Run the full frontend suite and build**

Run:

```bash
cd webui
npm test
npm run build
```

Expected: all tests pass and Vite emits a production bundle.

### Task 5: Browser regression and handoff

**Files:**
- Modify only files found defective by browser verification.

- [ ] **Step 1: Capture desktop Dashboard and Chat List**

Run the Vite server and use Agent Browser at 1280×720. Verify the header,
filter dock, empty/data states, active navigation, and absence of duplicate
navigation.

- [ ] **Step 2: Capture mobile Dashboard and Chat List**

Set the viewport to 390×844. Verify no page-level horizontal overflow,
44 px controls, wrapped app navigation, and chat cards instead of the table.

- [ ] **Step 3: Check runtime quality**

Inspect the browser console, click Dashboard/Chats navigation, open BotPicker,
and verify no uncaught errors.

- [ ] **Step 4: Re-run final gates**

Run:

```bash
cd webui
npm test
npm run build
git diff --check
```

Expected: tests and build pass with no whitespace errors.

- [ ] **Step 5: Commit**

Commit only the visual-system documents and WebUI changes with a focused
message. Do not add `.superpowers/` or unrelated generated files.

