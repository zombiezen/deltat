select
  uuidhex("uuid") as "uuid",
  "start_time" as "start_time",
  "end_time" as "end_time"
from "entries"
where task_uuid = uuid(:task_uuid)
order by "start_time" asc;
