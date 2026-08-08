/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { zodResolver } from "@hookform/resolvers/zod";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { useForm } from "react-hook-form";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import * as z from "zod";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";

import { getRelayLogPipelineStatus, startRelayLogFallbackReplay } from "../api";
import {
  SettingsForm,
  SettingsControlGroup,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from "../components/settings-form-layout";
import { SettingsPageFormActions } from "../components/settings-page-context";
import { useSettingsSaveConfirmation } from "../components/settings-save-confirmation";
import { SettingsSection } from "../components/settings-section";
import { useUpdateOption } from "../hooks/use-update-option";
import type { RelayLogReplayState, RelayLogReplayStatus } from "../types";
import {
  numberInputNoSpinnerClassName,
  safeNumberFieldProps,
} from "../utils/numeric-field";

const pipelineSchema = z.object({
  enabled: z.boolean(),
  consume_buf_max_entries: z.number().int().min(1).max(1_000_000),
  error_buf_max_entries: z.number().int().min(1).max(1_000_000),
  continuation_buf_max_entries: z.number().int().min(1).max(100_000),
  outer_batch_size: z.number().int().min(1).max(10_000),
  inner_batch_size: z.number().int().min(1).max(10_000),
  flush_max_per_cycle: z.number().int().min(1).max(100_000),
  full_drain: z.boolean(),
  flush_interval_ms: z.number().int().min(1).max(60_000),
  write_timeout_sec: z.number().int().min(1).max(30),
  fallback_queue_capacity: z.number().int().min(1).max(100_000),
  fallback_max_file_size_mb: z.number().int().min(1),
  fallback_max_files: z.number().int().min(1),
  shutdown_timeout_sec: z.number().int().min(1),
});

const retrySchema = z.object({
  retry_flush_interval_ms: z.number().int().min(1),
  max_retries: z.number().int().min(1).max(100),
  retry_buf_max_entries: z.number().int().min(1).max(1_000_000),
  allow_concurrent_flush: z.boolean(),
  circuit_failure_threshold: z.number().int().min(1),
  circuit_open_sec: z.number().int().min(1),
});

const schema = z.object({
  relay_log_pipeline_setting: pipelineSchema,
  relay_log_retry_setting: retrySchema,
});
type FormValues = z.infer<typeof schema>;
type PipelineSettings = FormValues["relay_log_pipeline_setting"];
type RetrySettings = FormValues["relay_log_retry_setting"];

export type RelayLogPipelineFlatDefaults = {
  [
    K in keyof PipelineSettings as `relay_log_pipeline_setting.${K & string}`
  ]: PipelineSettings[K];
} & {
  [
    K in keyof RetrySettings as `relay_log_retry_setting.${K & string}`
  ]: RetrySettings[K];
};

const pipelineKeys = Object.keys(pipelineSchema.shape) as Array<
  keyof PipelineSettings
>;
const retryKeys = Object.keys(retrySchema.shape) as Array<keyof RetrySettings>;

function buildFormDefaults(d: RelayLogPipelineFlatDefaults): FormValues {
  const pipeline = {} as PipelineSettings;
  const retry = {} as RetrySettings;
  for (const key of pipelineKeys) {
    (pipeline as Record<string, boolean | number>)[key] =
      d[`relay_log_pipeline_setting.${key}`];
  }
  for (const key of retryKeys) {
    (retry as Record<string, boolean | number>)[key] =
      d[`relay_log_retry_setting.${key}`];
  }
  return {
    relay_log_pipeline_setting: pipeline,
    relay_log_retry_setting: retry,
  };
}

function normalize(values: FormValues): RelayLogPipelineFlatDefaults {
  const result = {} as RelayLogPipelineFlatDefaults;
  for (const key of pipelineKeys) {
    (result as Record<string, boolean | number>)[
      `relay_log_pipeline_setting.${key}`
    ] = values.relay_log_pipeline_setting[key];
  }
  for (const key of retryKeys) {
    (result as Record<string, boolean | number>)[
      `relay_log_retry_setting.${key}`
    ] = values.relay_log_retry_setting[key];
  }
  return result;
}

type NumberField = {
  group: "pipeline" | "retry";
  section: "write" | "buffer" | "fallback" | "retry";
  name: string;
  label: string;
  description: string;
  min: number;
  max?: number;
};

const fields: NumberField[] = [
  {
    group: "pipeline",
    section: "write",
    name: "flush_interval_ms",
    label: "Log Flush Interval (ms)",
    description:
      "Flush when this interval elapses; the default is 5000 ms. This setting applies equally to PostgreSQL, MySQL, and SQLite.",
    min: 1,
    max: 60000,
  },
  {
    group: "pipeline",
    section: "write",
    name: "flush_max_per_cycle",
    label: "Log Flush Max Per Cycle",
    description:
      "Maximum log records drained in one cycle unless full drain is enabled.",
    min: 1,
    max: 100000,
  },
  {
    group: "pipeline",
    section: "write",
    name: "outer_batch_size",
    label: "Log Outer Batch Size",
    description: "Records handled per write iteration and retry unit.",
    min: 1,
    max: 10000,
  },
  {
    group: "pipeline",
    section: "write",
    name: "inner_batch_size",
    label: "Log SQL Batch Size",
    description:
      "Rows per database INSERT statement. Keep this at or below the outer batch size.",
    min: 1,
    max: 10000,
  },
  {
    group: "pipeline",
    section: "buffer",
    name: "consume_buf_max_entries",
    label: "Consumption Log Buffer",
    description: "Maximum pending consumption logs kept in memory.",
    min: 1,
    max: 1000000,
  },
  {
    group: "pipeline",
    section: "buffer",
    name: "error_buf_max_entries",
    label: "Error Log Buffer",
    description: "Maximum pending relay error logs kept in memory.",
    min: 1,
    max: 1000000,
  },
  {
    group: "pipeline",
    section: "buffer",
    name: "continuation_buf_max_entries",
    label: "Accounting Continuation Buffer",
    description:
      "Capacity for deferred accounting work after a log event is accepted or degraded. Takes effect immediately; a larger value holds more events in memory.",
    min: 1,
    max: 100000,
  },
  {
    group: "pipeline",
    section: "write",
    name: "write_timeout_sec",
    label: "Log Write Timeout (s)",
    description: "Deadline for one background database write attempt.",
    min: 1,
    max: 30,
  },
  {
    group: "pipeline",
    section: "fallback",
    name: "fallback_queue_capacity",
    label: "Fallback Queue Capacity",
    description:
      "Capacity of the queue that writes failed records to fallback files. Takes effect immediately; a larger value holds more events in memory.",
    min: 1,
    max: 100000,
  },
  {
    group: "pipeline",
    section: "fallback",
    name: "fallback_max_file_size_mb",
    label: "Fallback File Size (MB)",
    description: "Rotate a fallback file after it reaches this size.",
    min: 1,
  },
  {
    group: "pipeline",
    section: "fallback",
    name: "fallback_max_files",
    label: "Fallback File Count",
    description: "Maximum rotated fallback files retained.",
    min: 1,
  },
  {
    group: "pipeline",
    section: "fallback",
    name: "shutdown_timeout_sec",
    label: "Log Pipeline Shutdown Timeout (s)",
    description:
      "Shared deadline for the final drain during graceful shutdown.",
    min: 1,
  },
  {
    group: "retry",
    section: "retry",
    name: "retry_flush_interval_ms",
    label: "Log Retry Interval (ms)",
    description: "How often the background retry queue is checked.",
    min: 1,
  },
  {
    group: "retry",
    section: "retry",
    name: "max_retries",
    label: "Log Max Retries",
    description:
      "Database retry attempts before the event is written to fallback storage.",
    min: 1,
    max: 100,
  },
  {
    group: "retry",
    section: "retry",
    name: "retry_buf_max_entries",
    label: "Log Retry Buffer",
    description: "Maximum failed log events retained for database retry.",
    min: 1,
    max: 1000000,
  },
  {
    group: "retry",
    section: "retry",
    name: "circuit_failure_threshold",
    label: "Circuit Failure Threshold",
    description:
      "Consecutive database failures required to open the log writer circuit.",
    min: 1,
  },
  {
    group: "retry",
    section: "retry",
    name: "circuit_open_sec",
    label: "Circuit Open Duration (s)",
    description:
      "How long writes go directly to fallback before a half-open probe.",
    min: 1,
  },
];

const fieldsBySection = {
  write: fields.filter((item) => item.section === "write"),
  buffer: fields.filter((item) => item.section === "buffer"),
  fallback: fields.filter((item) => item.section === "fallback"),
  retry: fields.filter((item) => item.section === "retry"),
} as const;

function formatNumber(value: number) {
  return new Intl.NumberFormat().format(value);
}

function formatTime(value: number) {
  return value ? new Date(value * 1000).toLocaleString() : "-";
}

const circuitStateLabels = {
  closed: "Circuit closed",
  open: "Circuit open",
  half_open: "Circuit half-open",
} as const;

const replayStateLabels: Record<RelayLogReplayState, string> = {
  idle: "Backfill idle",
  running: "Backfill running",
  succeeded: "Backfill succeeded",
  partial_failed: "Backfill partially failed",
  failed: "Backfill failed",
};

const replayErrorMessages: Record<string, string> = {
  relay_log_replay_failed:
    "Some fallback logs could not be restored. Check the log database, then start backfill again.",
  relay_log_replay_shutdown:
    "Backfill stopped because the service is shutting down. Remaining fallback files were kept.",
  relay_log_replay_file_recovery_failed:
    "Fallback files could not be prepared. Check fallback directory permissions, then try again.",
  relay_log_replay_unexpected:
    "Backfill stopped unexpectedly. Remaining fallback files were kept; check server logs and try again.",
};

function getReplayErrorMessage(replay: RelayLogReplayStatus) {
  if (!replay.last_error) return "";
  return (
    replayErrorMessages[replay.last_error] ??
    "Backfill did not finish. Remaining fallback files were kept; check server logs and try again."
  );
}

export function RelayLogPipelineSection({
  defaultValues,
}: {
  defaultValues: RelayLogPipelineFlatDefaults;
}) {
  const { t } = useTranslation();
  const updateOption = useUpdateOption();
  const requestSaveConfirmation = useSettingsSaveConfirmation();
  const [showReplayDialog, setShowReplayDialog] = useState(false);
  const [isStartingReplay, setIsStartingReplay] = useState(false);
  const statusQuery = useQuery({
    queryKey: ["relay-log-pipeline-status"],
    queryFn: getRelayLogPipelineStatus,
    refetchInterval: (query) =>
      query.state.data?.data.replay.state === "running" ? 2000 : 10000,
  });
  const formDefaults = useMemo(
    () => buildFormDefaults(defaultValues),
    [defaultValues],
  );
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: formDefaults,
  });
  const baselineRef = useRef(defaultValues);
  const baselineSerializedRef = useRef(JSON.stringify(defaultValues));

  useEffect(() => {
    const serialized = JSON.stringify(defaultValues);
    if (serialized === baselineSerializedRef.current) return;
    baselineRef.current = defaultValues;
    baselineSerializedRef.current = serialized;
    form.reset(buildFormDefaults(defaultValues));
  }, [defaultValues, form]);

  const onSubmit = async (values: FormValues) => {
    const next = normalize(values);
    const changed = (
      Object.keys(next) as Array<keyof RelayLogPipelineFlatDefaults>
    ).filter((key) => next[key] !== baselineRef.current[key]);
    if (changed.length === 0) {
      toast.info(t("No changes to save"));
      return;
    }

    const disablesPipeline =
      baselineRef.current["relay_log_pipeline_setting.enabled"] &&
      !next["relay_log_pipeline_setting.enabled"];
    const enablesConcurrentFlush =
      !baselineRef.current[
        "relay_log_retry_setting.allow_concurrent_flush"
      ] && next["relay_log_retry_setting.allow_concurrent_flush"];
    const confirmationOptions =
      disablesPipeline || enablesConcurrentFlush
        ? {
            description: (
              <div className="space-y-2">
                {disablesPipeline ? (
                  <p>
                    {t(
                      "Disabling the relay log pipeline stops recording new relay consumption and error logs, and they cannot be backfilled later. Quota settlement is not affected.",
                    )}
                  </p>
                ) : null}
                {enablesConcurrentFlush ? (
                  <p>
                    {t(
                      "Enabling concurrent relay log flushes may increase PostgreSQL connection contention.",
                    )}
                  </p>
                ) : null}
              </div>
            ),
          }
        : undefined;

    await requestSaveConfirmation(async () => {
      for (const key of changed) {
        await updateOption.mutateAsync({ key, value: next[key] });
      }
      baselineRef.current = next;
      baselineSerializedRef.current = JSON.stringify(next);
      form.reset(buildFormDefaults(next));
    }, confirmationOptions);
  };

  const status = statusQuery.data?.data;
  const replay = status?.replay;
  const replayRunning = replay?.state === "running";
  const replayErrorMessage = replay ? getReplayErrorMessage(replay) : "";
  const replayDisabled =
    !replay || replayRunning || replay.pending_files === 0 || isStartingReplay;
  const queues = status
    ? [
        {
          key: "consume",
          label: "Consumption log queue",
          value: status.consume,
        },
        { key: "error", label: "Error log queue", value: status.error },
        { key: "retry", label: "Log retry queue", value: status.retry },
      ]
    : [];

  const handleStartReplay = async () => {
    setIsStartingReplay(true);
    try {
      const result = await startRelayLogFallbackReplay();
      if (!result.success) {
        const refreshed = await statusQuery.refetch();
        if (refreshed.data?.data.replay.state === "running") {
          toast.info(t("A fallback backfill task is already running."));
        } else {
          toast.error(
            t("Backfill could not be started. Refresh status and try again."),
          );
        }
        return;
      }
      if (result.data?.started) {
        toast.success(t("Fallback backfill started."));
      } else {
        toast.info(t("There are no fallback logs to backfill."));
      }
      setShowReplayDialog(false);
      await statusQuery.refetch();
    } catch {
      toast.error(
        t("Backfill could not be started. Refresh status and try again."),
      );
    } finally {
      setIsStartingReplay(false);
    }
  };

  return (
    <SettingsSection title={t("Relay Log Pipeline")}>
      {statusQuery.isError ? (
        <div className="border-destructive/30 bg-destructive/5 text-destructive rounded-xl border p-4 text-sm">
          {t(
            "Runtime status is temporarily unavailable. Configuration can still be saved.",
          )}
        </div>
      ) : null}
      <div className="grid gap-3 sm:grid-cols-3">
        {queues.map((queue) => (
          <div
            key={queue.key}
            className="bg-muted/20 rounded-xl border p-4 text-sm"
          >
            <div className="font-medium">{t(queue.label)}</div>
            <div className="text-muted-foreground mt-2 space-y-1">
              <div>
                {t("Backlog")}: {formatNumber(queue.value.backlog)} /{" "}
                {formatNumber(queue.value.capacity)}
              </div>
              <div>
                {t("Dropped")}: {formatNumber(queue.value.dropped)}
              </div>
            </div>
          </div>
        ))}
      </div>
      {status ? (
        <div className="bg-muted/20 text-muted-foreground rounded-xl border p-4 text-sm">
          <div>
            {t("Circuit state")}: {t(circuitStateLabels[status.circuit_state])}
          </div>
          <div>
            {t("Persisted logs")}: {formatNumber(status.persisted_total)}
          </div>
          <div>
            {t("Fallback records")}: {formatNumber(status.fallback_total)} (
            {t("pending")}: {formatNumber(status.fallback_backlog)})
          </div>
          <div>
            {t("Database timeouts")}: {formatNumber(status.db_timeout_total)}
          </div>
          <div>
            {t("Accounting continuation backlog")}:{" "}
            {formatNumber(status.continuation_backlog)}
          </div>
          <div>
            {t("Last successful write")}: {formatTime(status.last_success_at)}
          </div>
          <div>
            {t("Last write error")}: {formatTime(status.last_error_at)}
          </div>
        </div>
      ) : null}
      {status &&
      (status.continuation_dropped > 0 || status.fallback_errors > 0) ? (
        <div className="border-destructive/30 bg-destructive/5 text-destructive rounded-xl border p-4 text-sm">
          <div className="font-medium">
            {t("Relay log pipeline needs attention")}
          </div>
          <div className="mt-2">
            {t("Dropped accounting continuations")}:{" "}
            {formatNumber(status.continuation_dropped)}
          </div>
          <div>
            {t("Fallback write errors")}: {formatNumber(status.fallback_errors)}
          </div>
          <div className="mt-2 text-xs">
            {t(
              "Check database availability and fallback directory permissions, then monitor whether these counters continue increasing.",
            )}
          </div>
        </div>
      ) : null}

      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel="Save relay log pipeline settings"
          />
          <SettingsControlGroup>
            <div>
              <h4 className="text-sm font-medium">
                {t("Fallback Manual Backfill")}
              </h4>
              <p className="text-muted-foreground text-sm">
                {t(
                  "Restore fallback logs only when an administrator starts the task. Backfill shares the single log database writer and does not block relay requests.",
                )}
              </p>
            </div>
            {replay ? (
              <div className="grid gap-2 text-sm sm:grid-cols-2">
                <div>
                  <span className="text-muted-foreground">
                    {t("Backfill status")}:
                  </span>{" "}
                  {t(replayStateLabels[replay.state])}
                </div>
                <div>
                  <span className="text-muted-foreground">
                    {t("Pending fallback files")}:
                  </span>{" "}
                  {formatNumber(replay.pending_files)}
                </div>
                <div>
                  <span className="text-muted-foreground">
                    {t("Processed records")}:
                  </span>{" "}
                  {formatNumber(replay.processed_total)}
                </div>
                <div>
                  <span className="text-muted-foreground">
                    {t("Failed records")}:
                  </span>{" "}
                  {formatNumber(replay.failed_total)}
                </div>
                <div>
                  <span className="text-muted-foreground">
                    {t("Backfill started at")}:
                  </span>{" "}
                  {formatTime(replay.started_at)}
                </div>
                <div>
                  <span className="text-muted-foreground">
                    {t("Backfill finished at")}:
                  </span>{" "}
                  {formatTime(replay.finished_at)}
                </div>
                {replay.running_file ? (
                  <div className="min-w-0 sm:col-span-2">
                    <span className="text-muted-foreground">
                      {t("Current fallback file")}:
                    </span>{" "}
                    <span className="break-all">{replay.running_file}</span>
                  </div>
                ) : null}
              </div>
            ) : (
              <p className="text-muted-foreground text-sm">
                {statusQuery.isPending
                  ? t("Loading backfill status...")
                  : t(
                      "Backfill status is temporarily unavailable. Refresh the page and try again.",
                    )}
              </p>
            )}
            {replayErrorMessage ? (
              <div className="border-destructive/30 bg-destructive/5 text-destructive rounded-lg border p-3 text-sm">
                {t(replayErrorMessage)}
              </div>
            ) : null}
            {replay && replay.pending_files === 0 && !replayRunning ? (
              <p className="text-muted-foreground text-sm">
                {t("There are no fallback logs to backfill.")}
              </p>
            ) : null}
            <div>
              <Button
                type="button"
                onClick={() => setShowReplayDialog(true)}
                disabled={replayDisabled}
              >
                {replayRunning || isStartingReplay
                  ? t("Backfilling...")
                  : t("Start backfill")}
              </Button>
            </div>
          </SettingsControlGroup>
          <SettingsControlGroup className="space-y-4">
            <div className="text-sm font-medium">{t("Pipeline behavior")}</div>
            <FormField
              control={form.control}
              name="relay_log_pipeline_setting.enabled"
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t("Enable Relay Log Pipeline")}</FormLabel>
                    <FormDescription>
                      {t(
                        "When disabled, relay responses stay non-blocking and log persistence is skipped while accounting continuation remains asynchronous.",
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <FormField
              control={form.control}
              name="relay_log_pipeline_setting.full_drain"
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t("Full Drain")}</FormLabel>
                    <FormDescription>
                      {t(
                        "Drain all pending logs each cycle. This can create a large database burst after an outage.",
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </SettingsControlGroup>
          <NumberSettingFieldGroup
            title={t("Batching and database writes")}
            items={fieldsBySection.write}
            form={form}
            t={t}
          />
          <NumberSettingFieldGroup
            title={t("Memory queues")}
            items={fieldsBySection.buffer}
            form={form}
            t={t}
          />
          <NumberSettingFieldGroup
            title={t("Fallback storage")}
            items={fieldsBySection.fallback}
            form={form}
            t={t}
          />
          <SettingsControlGroup className="space-y-4">
            <div className="text-sm font-medium">{t("Retry Queue")}</div>
            <FormField
              control={form.control}
              name="relay_log_retry_setting.allow_concurrent_flush"
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t("Allow Concurrent Log Flush")}</FormLabel>
                    <FormDescription>
                      {t(
                        "Allow the retry writer to run beside the main writer. Leave this off to minimize connection-pool pressure.",
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <div className="grid gap-x-5 gap-y-6 lg:grid-cols-2">
              {fieldsBySection.retry.map((item) => (
                <NumberSettingField
                  key={item.name}
                  item={item}
                  form={form}
                  t={t}
                />
              ))}
            </div>
          </SettingsControlGroup>
        </SettingsForm>
      </Form>
      <AlertDialog open={showReplayDialog} onOpenChange={setShowReplayDialog}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("Start fallback log backfill?")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                "Backfill will restore fallback logs in small batches using the single log database writer. Relay requests will not wait for this task. Remaining files are kept if the task fails.",
              )}{" "}
              {t("{{count}} fallback files are currently pending.", {
                count: replay?.pending_files ?? 0,
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={isStartingReplay}>
              {t("Cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={(event) => {
                event.preventDefault();
                void handleStartReplay();
              }}
              disabled={replayDisabled}
            >
              {isStartingReplay ? t("Starting...") : t("Start backfill")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsSection>
  );
}

function NumberSettingFieldGroup({
  title,
  items,
  form,
  t,
}: {
  title: string;
  items: readonly NumberField[];
  form: ReturnType<typeof useForm<FormValues>>;
  t: (key: string) => string;
}) {
  return (
    <SettingsControlGroup className="space-y-4">
      <div className="text-sm font-medium">{title}</div>
      <div className="grid gap-x-5 gap-y-6 lg:grid-cols-2">
        {items.map((item) => (
          <NumberSettingField key={item.name} item={item} form={form} t={t} />
        ))}
      </div>
    </SettingsControlGroup>
  );
}

function NumberSettingField({
  item,
  form,
  t,
}: {
  item: NumberField;
  form: ReturnType<typeof useForm<FormValues>>;
  t: (key: string) => string;
}) {
  const name =
    `${item.group === "pipeline" ? "relay_log_pipeline_setting" : "relay_log_retry_setting"}.${item.name}` as
      | `relay_log_pipeline_setting.${keyof PipelineSettings}`
      | `relay_log_retry_setting.${keyof RetrySettings}`;
  return (
    <FormField
      control={form.control}
      name={name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{t(item.label)}</FormLabel>
          <FormControl>
            <Input
              className={numberInputNoSpinnerClassName}
              type="number"
              inputMode="numeric"
              min={item.min}
              max={item.max}
              step={1}
              {...safeNumberFieldProps(field)}
            />
          </FormControl>
          <FormDescription>{t(item.description)}</FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  );
}
