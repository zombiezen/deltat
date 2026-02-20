delete from "entries"
where
  :all or (
    coalesce("start_time" >= strftime('%FT%T', :min_time), true) and
    coalesce("start_time" < strftime('%FT%T', :max_time), true) and
    coalesce("end_time" >= strftime('%FT%T', :min_time), true) and
    coalesce("end_time" <= strftime('%FT%T', :max_time), true) and
    coalesce("task_uuid" = uuid(:task_uuid), true)
  );
