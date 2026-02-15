insert into "entries" (
  "uuid",
  "task_uuid",
  "start_time",
  "end_time",
  "scheduled_end_time"
) values (
  uuid(:uuid),
  uuid(:task_uuid),
  strftime('%FT%T', :started_at),
  strftime('%FT%T', :ended_at),
  strftime('%FT%T', :scheduled_end_time)
);
