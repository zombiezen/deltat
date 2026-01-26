update "entries"
set "task_uuid" = uuid(:task_uuid)
where "uuid" = uuid(:uuid);
