insert into "tasks" (
  "uuid",
  "description",
  "created_at"
) values (
  uuid(:uuid),
  coalesce(:description, ''),
  strftime('%FT%T', :created_at)
);
