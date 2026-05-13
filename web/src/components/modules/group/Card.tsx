'use client';

import { useState, useMemo, useCallback } from 'react';
import { Trash2, X, Pencil, Loader2 } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import {
    type Group,
    type GroupSummary,
    type GroupUpdateRequest,
    useDeleteGroup,
    useUpdateGroup,
    useGroupDetail,
} from '@/api/endpoints/group';
import { useModelChannelList } from '@/api/endpoints/model';
import { useTranslations } from 'next-intl';
import { toast } from '@/components/common/Toast';
import { CopyIconButton } from '@/components/common/CopyButton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/animate-ui/components/animate/tooltip';
import type { SelectedMember } from './ItemList';
import { GroupEditor, type GroupEditorValues } from './Editor';
import { buildChannelNameByModelKey, modelChannelKey } from './utils';
import {
    MorphingDialog,
    MorphingDialogClose,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTitle,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

interface EditDialogContentProps {
    group: Group;
    displayMembers: SelectedMember[];
    editorKey: number;
    isSubmitting: boolean;
    onSubmit: (values: GroupEditorValues, onDone?: () => void) => void;
}

function EditDialogContent({ group, displayMembers, editorKey, isSubmitting, onSubmit }: EditDialogContentProps) {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('group');
    return (
        <>
            <MorphingDialogTitle className="shrink-0">
                <header className="mb-3 flex items-center justify-between">
                    <h2 className="text-2xl font-bold text-card-foreground">
                        {t('detail.actions.edit')}
                    </h2>
                    <MorphingDialogClose className="relative right-0 top-0" />
                </header>
            </MorphingDialogTitle>
            <MorphingDialogDescription className="flex-1 min-h-0 overflow-hidden">
                <GroupEditor
                    key={`edit-group-${group.id}-${editorKey}`}
                    initial={{
                        name: group.name,
                        match_regex: group.match_regex ?? '',
                        mode: group.mode,
                        first_token_time_out: group.first_token_time_out ?? 0,
                        session_keep_time: group.session_keep_time ?? 0,
                        members: displayMembers,
                    }}
                    submitText={t('detail.actions.save')}
                    submittingText={t('create.submitting')}
                    isSubmitting={isSubmitting}
                    onCancel={() => setIsOpen(false)}
                    onSubmit={(v) => onSubmit(v, () => setIsOpen(false))}
                />
            </MorphingDialogDescription>
        </>
    );
}

function GroupDetailEditBody({ groupId }: { groupId: number }) {
    const { isOpen } = useMorphingDialog();
    const t = useTranslations('group');
    const { data: group, isLoading, isError, error, refetch, dataUpdatedAt } = useGroupDetail(groupId, { enabled: isOpen });
    const updateGroup = useUpdateGroup();
    const { data: modelChannels = [] } = useModelChannelList();

    const channelNameByKey = useMemo(() => buildChannelNameByModelKey(modelChannels), [modelChannels]);
    const enabledByKey = useMemo(() => {
        const map = new Map<string, boolean>();
        modelChannels.forEach((mc) => {
            map.set(modelChannelKey(mc.channel_id, mc.name), mc.enabled);
        });
        return map;
    }, [modelChannels]);

    const displayMembers = useMemo((): SelectedMember[] => {
        if (!group?.items?.length) return [];
        return [...group.items]
            .sort((a, b) => a.priority - b.priority)
            .map((item) => ({
                id: modelChannelKey(item.channel_id, item.model_name),
                name: item.model_name,
                enabled: enabledByKey.get(modelChannelKey(item.channel_id, item.model_name)) ?? true,
                channel_id: item.channel_id,
                channel_name: channelNameByKey.get(modelChannelKey(item.channel_id, item.model_name)) ?? `Channel ${item.channel_id}`,
                item_id: item.id,
                weight: item.weight,
            }));
    }, [group?.items, channelNameByKey, enabledByKey]);

    const onSuccess = useCallback(() => toast.success(t('toast.updated')), [t]);
    const onError = useCallback((err: Error) => toast.error(t('toast.updateFailed'), { description: err.message }), [t]);

    const handleSubmitEdit = useCallback(
        (values: GroupEditorValues, onDone?: () => void) => {
            if (!group?.id) return;

            const originalItems = [...(group.items || [])].sort((a, b) => a.priority - b.priority);
            const originalById = new Map<number, { priority: number; weight: number }>();
            const originalIds = new Set<number>();
            originalItems.forEach((it) => {
                if (typeof it.id === 'number') {
                    originalIds.add(it.id);
                    originalById.set(it.id, { priority: it.priority, weight: it.weight });
                }
            });

            const newIds = new Set<number>();
            values.members.forEach((m) => {
                if (typeof m.item_id === 'number') newIds.add(m.item_id);
            });

            const items_to_delete = Array.from(originalIds).filter((id) => !newIds.has(id));

            const items_to_add = values.members
                .map((m, idx) => ({ m, priority: idx + 1 }))
                .filter(({ m }) => typeof m.item_id !== 'number')
                .map(({ m, priority }) => ({
                    channel_id: m.channel_id,
                    model_name: m.name,
                    priority,
                    weight: m.weight ?? 1,
                }));

            const items_to_update = values.members
                .map((m, idx) => ({ m, priority: idx + 1 }))
                .filter(({ m }) => typeof m.item_id === 'number')
                .map(({ m, priority }) => {
                    const id = m.item_id!;
                    const orig = originalById.get(id);
                    const weight = m.weight ?? 1;
                    if (!orig) return null;
                    if (orig.priority === priority && orig.weight === weight) return null;
                    return { id, priority, weight };
                })
                .filter((x): x is { id: number; priority: number; weight: number } => x !== null);

            const payload: GroupUpdateRequest = { id: group.id };
            const nextName = values.name.trim();
            const nextRegex = (values.match_regex ?? '').trim();
            const nextFirstTokenTimeOut = values.first_token_time_out ?? 0;
            const nextSessionKeepTime = values.session_keep_time ?? 0;

            if (nextName && nextName !== group.name) payload.name = nextName;
            if (values.mode !== group.mode) payload.mode = values.mode;
            if (nextRegex !== (group.match_regex ?? '')) payload.match_regex = nextRegex;
            if (nextFirstTokenTimeOut !== (group.first_token_time_out ?? 0)) payload.first_token_time_out = nextFirstTokenTimeOut;
            if (nextSessionKeepTime !== (group.session_keep_time ?? 0)) payload.session_keep_time = nextSessionKeepTime;
            if (items_to_add.length) payload.items_to_add = items_to_add;
            if (items_to_update.length) payload.items_to_update = items_to_update;
            if (items_to_delete.length) payload.items_to_delete = items_to_delete;

            if (Object.keys(payload).length === 1) {
                onDone?.();
                return;
            }

            updateGroup.mutate(payload, {
                onSuccess: () => {
                    onSuccess();
                    onDone?.();
                },
                onError,
            });
        },
        [group, onSuccess, onError, updateGroup]
    );

    if (isLoading || !group) {
        return (
            <div className="flex flex-col items-center justify-center gap-3 py-16 text-muted-foreground">
                <Loader2 className="size-8 animate-spin" aria-hidden />
                <span className="text-sm">{t('detail.loading')}</span>
            </div>
        );
    }

    if (isError) {
        return (
            <div className="flex flex-col items-center justify-center gap-3 py-12 px-4 text-center">
                <p className="text-sm text-destructive">{error?.message ?? t('detail.loadFailed')}</p>
                <button
                    type="button"
                    onClick={() => refetch()}
                    className="text-sm font-medium text-primary hover:underline"
                >
                    {t('detail.retry')}
                </button>
            </div>
        );
    }

    return (
        <EditDialogContent
            group={group}
            displayMembers={displayMembers}
            editorKey={dataUpdatedAt}
            isSubmitting={updateGroup.isPending}
            onSubmit={handleSubmitEdit}
        />
    );
}

export function GroupCard({ summary }: { summary: GroupSummary }) {
    const t = useTranslations('group');
    const deleteGroup = useDeleteGroup();

    const [confirmDelete, setConfirmDelete] = useState(false);

    return (
        <article className="flex flex-col rounded-3xl border border-border bg-card text-card-foreground p-4 custom-shadow">
            <header className="flex items-start justify-between relative overflow-visible rounded-xl -mx-1 px-1 -my-1 py-1">
                <div className="relative flex-1 mr-2 min-w-0 group/title">
                    <Tooltip side="top" sideOffset={10} align="center">
                        <TooltipTrigger asChild>
                            <h3 className="text-lg font-bold truncate">{summary.name}</h3>
                        </TooltipTrigger>
                        <TooltipContent key={summary.name}>{summary.name}</TooltipContent>
                    </Tooltip>
                </div>

                <div className="flex items-center gap-1 shrink-0">
                    <MorphingDialog>
                        <MorphingDialogTrigger className="p-1.5 rounded-lg transition-colors hover:bg-muted text-muted-foreground hover:text-foreground">
                            <Tooltip side="top" sideOffset={10} align="center">
                                <TooltipTrigger asChild>
                                    <Pencil className="size-4" />
                                </TooltipTrigger>
                                <TooltipContent>{t('detail.actions.edit')}</TooltipContent>
                            </Tooltip>
                        </MorphingDialogTrigger>

                        <MorphingDialogContainer>
                            <MorphingDialogContent className="relative w-screen max-w-full md:max-w-4xl bg-card text-card-foreground px-6 py-4 rounded-3xl h-[calc(100vh-2rem)] flex flex-col overflow-hidden">
                                <GroupDetailEditBody groupId={summary.id} />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>

                    <Tooltip side="top" sideOffset={10} align="center">
                        <TooltipTrigger>
                            <CopyIconButton
                                text={summary.name}
                                className="p-1.5 rounded-lg transition-colors hover:bg-muted text-muted-foreground hover:text-foreground"
                                copyIconClassName="size-4"
                                checkIconClassName="size-4 text-primary"
                            />
                        </TooltipTrigger>
                        <TooltipContent>{t('detail.actions.copyName')}</TooltipContent>
                    </Tooltip>
                    {!confirmDelete && (
                        <Tooltip side="top" sideOffset={10} align="center">
                            <TooltipTrigger>
                                <motion.button
                                    layoutId={`delete-btn-group-${summary.id}`}
                                    type="button"
                                    onClick={() => setConfirmDelete(true)}
                                    className="p-1.5 rounded-lg hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors"
                                >
                                    <Trash2 className="size-4" />
                                </motion.button>
                            </TooltipTrigger>
                            <TooltipContent>{t('detail.actions.delete')}</TooltipContent>
                        </Tooltip>
                    )}
                </div>

                <AnimatePresence>
                    {confirmDelete && (
                        <motion.div
                            layoutId={`delete-btn-group-${summary.id}`}
                            className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-2 rounded-xl"
                            transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                        >
                            <button
                                type="button"
                                onClick={() => setConfirmDelete(false)}
                                className="flex h-7 w-7 items-center justify-center rounded-lg bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                            >
                                <X className="size-4" />
                            </button>
                            <button
                                type="button"
                                onClick={() =>
                                    deleteGroup.mutate(summary.id, {
                                        onSuccess: () => toast.success(t('toast.deleted')),
                                    })
                                }
                                disabled={deleteGroup.isPending}
                                className="flex-1 h-7 flex items-center justify-center gap-2 rounded-lg bg-destructive-foreground text-destructive text-sm font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                                <Trash2 className="size-3.5" />
                                {t('detail.actions.confirmDelete')}
                            </button>
                        </motion.div>
                    )}
                </AnimatePresence>
            </header>
        </article>
    );
}
