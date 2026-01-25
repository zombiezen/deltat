insert into "entries" (
  "uuid",
  "task_uuid",
  "start_time",
  "end_time"
) values (
  uuid7(),
  uuid(:task_uuid),
  strftime('%FT%T', :started_at),
  strftime('%FT%T', :ended_at)
) returning uuidhex("uuid") as "uuid";
