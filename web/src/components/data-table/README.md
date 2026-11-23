# Data Table Components

This package keeps a stable public API through `index.ts`; feature code should
continue importing from `@/components/data-table`.

- `core/`: TanStack table rendering primitives, headers, rows, pagination,
  loading, empty states, and pinned-column behavior.
- `layout/`: responsive page-level composition that combines toolbar, desktop
  table, mobile list, bulk actions, and pagination placement.
- `toolbar/`: filter/search/view-option controls and selection action toolbar.
- `static/`: lightweight table rendering for local/static arrays that do not
  need TanStack state.
- `hooks/`: table state and filter hooks.

Keep feature-specific columns, actions, and dialogs inside their feature
folders. Shared table code belongs here only when it is reusable across more
than one feature.

For cursor APIs, control the table's `pagination` state, set `pageCount: -1`,
and pass `cursorPagination={{ hasNextPage, isFetching }}` to `DataTablePage`.
The shared footer shows the current page, returned row count, fixed page size,
and first/previous/next controls without claiming a total or offering last-page
access. Pass `paginationInFooter={false}` when the table lives in a dialog.

In the default layout, cursor callers whose API accepts a variable limit can opt
in with `cursorPagination.allowPageSizeChange`, handling size changes by resetting
their cursor history. `numberedPages` reuses numbered buttons for previously visited
pages and the available next page. These options do not imply a known total or
enable navigation to an unknown last page. Other cursor callers keep fixed page
sizes and the existing page label. Compact layouts retain their compact controls.
