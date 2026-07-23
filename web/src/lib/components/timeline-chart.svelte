<script lang="ts">
	// Bucketed bar timeline: red/amber slices stack at the bottom of each bar.
	// Click a bar to select its bucket, click again to clear.
	let {
		buckets,
		start,
		end,
		selected = $bindable(-1),
		color = '#8b8bef',
		format,
		label
	}: {
		buckets: { total: number; red: number; amber: number }[];
		start: number;
		end: number;
		selected?: number;
		color?: string;
		format: (t: number) => string;
		label: string;
	} = $props();

	const max = $derived(Math.max(1, ...buckets.map((b) => b.total)));
	const span = $derived(Math.max(1, (end - start) / buckets.length));
</script>

<div class="rounded-lg border p-3">
	<svg viewBox="0 0 {buckets.length * 10} 64" preserveAspectRatio="none" class="h-16 w-full" role="img" aria-label={label}>
		{#each buckets as b, i (i)}
			{@const h = (b.total / max) * 56}
			{@const hr = b.total ? (b.red / b.total) * h : 0}
			{@const ha = b.total ? (b.amber / b.total) * h : 0}
			<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
			<g
				class="cursor-pointer"
				opacity={selected === -1 || selected === i ? 1 : 0.35}
				onclick={() => (selected = selected === i ? -1 : i)}
			>
				<rect x={i * 10 + 1.5} y="0" width="7" height="64" fill="transparent" />
				{#if b.total}
					<rect x={i * 10 + 1.5} y={60 - h} width="7" height={h - hr - ha} rx="1" fill={color} />
					{#if ha}<rect x={i * 10 + 1.5} y={60 - hr - ha} width="7" height={ha} fill="#f59e0b" />{/if}
					{#if hr}<rect x={i * 10 + 1.5} y={60 - hr} width="7" height={hr} fill="#ef4444" />{/if}
				{:else}
					<rect x={i * 10 + 1.5} y="58" width="7" height="2" rx="1" fill={color} opacity="0.35" />
				{/if}
			</g>
		{/each}
	</svg>
	<div class="text-muted-foreground flex justify-between font-mono text-[10px]">
		<span>{format(start)}</span>
		<span>{format(start + (end - start) / 2)}</span>
		<span>{format(end)}</span>
	</div>
	{#if selected >= 0}
		<button class="text-muted-foreground hover:text-foreground mt-1 text-xs underline" onclick={() => (selected = -1)}>
			Showing {format(start + selected * span)} to {format(start + (selected + 1) * span)}, click to clear
		</button>
	{/if}
</div>
