select
  "start_time" as "start_time",
  "end_time" as "end_time"
from "entries"
where "uuid" = uuid(:uuid);
