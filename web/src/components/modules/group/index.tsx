'use client';

import { useMemo } from 'react';
import { GroupCard } from './Card';
import { useGroupList, type GroupSummary } from '@/api/endpoints/group';
import { useSearchStore, useToolbarViewOptionsStore } from '@/components/modules/toolbar';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';

export function Group() {
    const { data: groups } = useGroupList();
    const pageKey = 'group' as const;
    const searchTerm = useSearchStore((s) => s.getSearchTerm(pageKey));
    const sortField = useToolbarViewOptionsStore((s) => s.getSortField(pageKey));
    const sortOrder = useToolbarViewOptionsStore((s) => s.getSortOrder(pageKey));
    const filter = useToolbarViewOptionsStore((s) => s.groupFilter);

    const sortedGroups = useMemo((): GroupSummary[] => {
        if (!groups) return [];
        return [...groups].sort((a, b) => {
            const diff = sortField === 'name'
                ? a.name.localeCompare(b.name)
                : (a.id || 0) - (b.id || 0);
            return sortOrder === 'asc' ? diff : -diff;
        });
    }, [groups, sortField, sortOrder]);

    const visibleGroups = useMemo(() => {
        const term = searchTerm.toLowerCase().trim();
        const byName = !term ? sortedGroups : sortedGroups.filter((g) => g.name.toLowerCase().includes(term));

        if (filter === 'with-members') return byName.filter((g) => g.item_count > 0);
        if (filter === 'empty') return byName.filter((g) => g.item_count === 0);

        return byName;
    }, [sortedGroups, searchTerm, filter]);

    return (
        <VirtualizedGrid<GroupSummary>
            items={visibleGroups}
            columns={{ default: 1, md: 2, lg: 3 }}
            estimateItemHeight={100}
            getItemKey={(group, index) => group.id ?? `group-${index}`}
            renderItem={(group) => <GroupCard summary={group} />}
        />
    );
}
