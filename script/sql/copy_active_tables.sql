\set ON_ERROR_STOP on

begin;

do $$
declare
  source_schema text := :'old_schema';
  target_schema text := :'new_schema';
  table_name_ text;
  table_list text[] := string_to_array(:'table_list', ',');
  column_list text;
  copied_rows bigint;
begin
  if coalesce(array_length(table_list, 1), 0) = 0 then
    raise exception 'table_list is empty';
  end if;

  foreach table_name_ in array table_list loop
    select string_agg(format('%I', column_name), ', ' order by ordinal_position)
      into column_list
    from information_schema.columns
    where table_schema = source_schema
      and table_name = table_name_;

    if column_list is null then
      raise exception 'source table %.% does not exist or has no columns', source_schema, table_name_;
    end if;

    execute format(
      'insert into %I.%I (%s) select %s from %I.%I',
      target_schema,
      table_name_,
      column_list,
      column_list,
      source_schema,
      table_name_
    );

    get diagnostics copied_rows = row_count;
    raise notice 'copied %.% -> %.%, rows=%', source_schema, table_name_, target_schema, table_name_, copied_rows;
  end loop;
end $$;

commit;
