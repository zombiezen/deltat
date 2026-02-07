update "entries"
set "task_uuid" = uuid(:new_task_uuid)
where "task_uuid" = uuid(:old_task_uuid);
