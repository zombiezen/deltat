create table "labels" (
  "id" integer primary key
    not null,
  "name" text
    not null
    unique
);

create table "tasks" (
  "uuid" blob primary key
    not null
    check (octet_length("uuid") = 16),
  "description" text
    not null
    default '',
  "created_at" text
    not null
    check ("created_at" regexp '[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]')
);

create table "task_labels" (
  "task_uuid" blob
    not null
    references "tasks" on delete cascade,
  "label_id" integer
    not null
    references "labels" on delete cascade,

  unique ("task_uuid", "label_id")
);

create table "entries" (
  "uuid" blob primary key
    not null
    check (octet_length("uuid") = 16),
  "task_uuid" blob
    not null
    references "tasks",
  "start_time" text
    not null
    check ("start_time" regexp '[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]'),
  "end_time" text
    check ("end_time" is null or "end_time" regexp '[0-9]{4}-(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]')
);

create index "entries_by_task" on "entries" ("task_uuid", "start_time" desc, "uuid");

create index "entries_reverse_chronological" on "entries" ("start_time" desc, "uuid");
