# WebUI Visual System Modernization Design

**Date:** 2026-07-31

**Status:** Approved by continuation of the existing warm Agent operations direction

**Scope:** The existing WebUI application shell, Dashboard, global filters,
Bot picker, chat list, chat detail statistics/configuration surfaces, and
their responsive behavior.

## Context

The Agentic rollout surfaces introduced a deliberate visual language: deep
pine framing, warm neutral surfaces, restrained lime actions, rounded cards,
and explicit operational state. Historical WebUI surfaces still use default
Element Plus blue, inline layout declarations, emoji navigation, duplicated
navigation, fixed four-column grids, and blank charts when no bot is selected.
The result looks like two products sharing one router.

## Goals

- Make every route feel like one Agent operations product.
- Preserve every API, route, filter, chart interaction, rollout guard, and
  configuration mutation.
- Replace duplicated navigation with one application-level navigation model.
- Establish reusable global design tokens and component-level layout hooks.
- Give empty, loading, error, and active states intentional presentation.
- Preserve dense desktop operations while making primary workflows usable at
  360 px, 768 px, 1024 px, and wide desktop widths.
- Keep controls at least 44 px tall on touch layouts and retain textual status
  labels instead of relying on color.

## Non-goals

- Changing analytics calculations or backend APIs.
- Replacing Element Plus or ECharts.
- Adding dark mode, user accounts, or a new navigation hierarchy.
- Rewriting the large view-model sections in Dashboard, ChatList, or
  ChatDetail when presentation-only changes are sufficient.

## Visual Direction

The product remains a **warm Agent operations desk**:

- deep pine (`#143b36`) is the structural color;
- warm canvas (`#f4f2ec`) replaces sterile white page backgrounds;
- paper surfaces use subtle green-grey borders and low, broad shadows;
- lime (`#d7ff73`) is reserved for primary Agentic actions and live accents;
- teal and amber communicate healthy and caution states;
- typography uses a native CJK-capable sans stack for reliable deployment,
  with tabular numerals and a monospace stack for identifiers.

The memorable element is a compact pine brand rail paired with layered paper
surfaces: operational and dense, but not a dark infrastructure console.

## Application Shell

`App.vue` owns the only global navigation. It contains:

- a CSS-drawn BetaGo mark, product name, and “Agent operations” descriptor;
- Dashboard and Chats navigation links with active text and icon state;
- a selected/healthy bot status capsule;
- a compact mobile layout that wraps navigation beneath the brand row.

The shell is sticky at the top, uses a translucent warm backdrop, and leaves
route content inside one fluid `max-width` workspace. Global Element Plus
tokens live in `styles/theme.css`, imported before application mount.

## Shared Control Surface

`GlobalFilterBar.vue` becomes a filter dock instead of a second navigation
bar. It groups Bot source, time window, primary metric, and secondary metric
under short labels. On narrow screens groups wrap and controls become
full-width where needed. The drill path remains directly below the controls.

`BotPicker.vue` adopts the same status language and surface treatment. Its
dropdown is fluid up to a safe desktop width, rows use 44 px interaction
targets, and its editor dialog uses a responsive width.

## Dashboard

The Dashboard begins with a route eyebrow, title, and concise description.
When no bot is selected, it shows a focused onboarding empty state instead of
rendering empty charts. With data, it uses:

- a four-card pine KPI band;
- a wide trend card;
- responsive 1/2/4-column donut grid;
- responsive 1/2-column analytical cards;
- consistent card borders, padding, and chart hints.

Chart colors move from generic blue-first defaults to the shared pine, teal,
lime, amber, coral, and slate palette.

## Chat List

The existing rollout behavior remains unchanged. Presentation changes:

- filter controls use semantic groups instead of inline flex declarations;
- distribution charts sit in a bounded analytical strip;
- desktop keeps the sortable table;
- below 768 px the table is replaced by touch-friendly chat cards containing
  identity, bot, Agentic summary, key metrics, and one clear detail action;
- empty results explain whether the cause is missing bots or active filters.

## Chat Detail

The page header becomes an identity card with bot, chat, membership, and owner
metadata. Tabs drop emoji labels and use the same navigation language as the
shell. Statistics retain their existing ECharts content but all fixed
four-column and 16/8 layouts progressively become 2-column and then stacked.
Config tables remain scrollable and Agentic cards retain their approved
two-column-to-single-column behavior.

## Accessibility and Motion

- Focus-visible rings use pine plus a pale lime halo.
- Buttons, inputs, segmented controls, and navigation links meet the 44 px
  mobile target.
- Page content enters with one short fade/translate sequence; loading
  directives and dialogs do not animate redundantly.
- `prefers-reduced-motion` disables decorative transitions.
- Status always includes text.

## Verification

- Source-level component tests protect the single-navigation shell, filter
  dock, Dashboard empty state, responsive chat-card fallback, and shared
  theme import.
- Existing Agentic behavior tests must remain green.
- `npm test` and `npm run build` must pass.
- Browser screenshots are captured at desktop and mobile widths for
  Dashboard and Chat List; console errors and horizontal overflow are checked.

