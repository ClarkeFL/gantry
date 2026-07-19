<script lang="ts">
	import * as Tooltip from '$lib/components/ui/tooltip';
	import CircleHelpIcon from '@lucide/svelte/icons/circle-help';

	let {
		text,
		label = 'More info'
	}: {
		text: string;
		label?: string;
	} = $props();
</script>

<Tooltip.Provider delayDuration={150}>
	<Tooltip.Root>
		<Tooltip.Trigger
			type="button"
			class="text-muted-foreground hover:text-foreground inline-flex size-5 shrink-0 items-center justify-center rounded-full transition-colors"
			aria-label={label}
		>
			<CircleHelpIcon class="size-3.5" />
		</Tooltip.Trigger>
		<Tooltip.Content side="top" class="max-w-xs text-left leading-snug font-normal normal-case">
			{text}
		</Tooltip.Content>
	</Tooltip.Root>
</Tooltip.Provider>
