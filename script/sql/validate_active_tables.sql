\set ON_ERROR_STOP on

drop table if exists pg_temp.migration_validation;
create temp table migration_validation (
  table_name text not null,
  old_count bigint not null,
  new_count bigint not null,
  counts_match boolean not null
) on commit drop;

do $$
declare
  source_schema text := :'old_schema';
  target_schema text := :'new_schema';
  table_name_ text;
  table_list text[] := string_to_array(:'table_list', ',');
  old_count_ bigint;
  new_count_ bigint;
begin
  if coalesce(array_length(table_list, 1), 0) = 0 then
    raise exception 'table_list is empty';
  end if;

  foreach table_name_ in array table_list loop
    execute format('select count(*) from %I.%I', source_schema, table_name_) into old_count_;
    execute format('select count(*) from %I.%I', target_schema, table_name_) into new_count_;

    insert into migration_validation(table_name, old_count, new_count, counts_match)
    values (table_name_, old_count_, new_count_, old_count_ = new_count_);
  end loop;
end $$;

table migration_validation;

drop table if exists pg_temp.prompt_template_seed_validation;
create temp table prompt_template_seed_validation (
  prompt_id bigint not null,
  old_count bigint not null,
  new_count bigint not null,
  counts_match boolean not null
) on commit drop;

do $$
declare
  source_schema text := :'old_schema';
  target_schema text := :'new_schema';
  prompt_id_ bigint;
  old_count_ bigint;
  new_count_ bigint;
begin
  if exists (
    select 1
    from information_schema.tables
    where table_schema = source_schema
      and table_name = 'prompt_template_args'
  ) and exists (
    select 1
    from information_schema.tables
    where table_schema = target_schema
      and table_name = 'prompt_template_args'
  ) then
    foreach prompt_id_ in array array[3, 5] loop
      execute format(
        'select count(*) from %I.prompt_template_args where prompt_id = %s',
        source_schema,
        prompt_id_
      ) into old_count_;
      execute format(
        'select count(*) from %I.prompt_template_args where prompt_id = %s',
        target_schema,
        prompt_id_
      ) into new_count_;

      insert into prompt_template_seed_validation(prompt_id, old_count, new_count, counts_match)
      values (prompt_id_, old_count_, new_count_, old_count_ = new_count_);
    end loop;
  end if;
end $$;

table prompt_template_seed_validation;

do $$
begin
  if exists (
    select 1
    from migration_validation
    where counts_match = false
  ) then
    raise exception 'table row count validation failed';
  end if;

  if exists (
    select 1
    from prompt_template_seed_validation
    where counts_match = false
  ) then
    raise exception 'prompt_template_args seed validation failed';
  end if;
end $$;
