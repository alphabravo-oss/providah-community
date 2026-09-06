import React, { useId, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import {
  tableFeatures,
  columnVisibilityFeature,
  rowSortingFeature,
  type SortingState,
  type ColumnVisibilityState,
  type OnChangeFn,
  useTable,
  flexRender,
  type RowData,
  type ColumnDef,
} from "@tanstack/react-table";
import { useForm } from "@tanstack/react-form";
import { z } from "zod";
import { message } from "./api";

export function ErrorNote({ error }: { error: unknown }) {
  return error ? (
    <div role="alert" className="error">
      {message(error)}
    </div>
  ) : null;
}
export function PageHeader({
  eyebrow,
  title,
  description,
  children,
}: {
  eyebrow: string;
  title: string;
  description: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="page-heading">
      <div>
        <div className="eyebrow">{eyebrow}</div>
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      {children}
    </div>
  );
}
export function Field({
  label,
  error,
  children,
}: {
  label: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
      {error && <small role="alert">{error}</small>}
    </label>
  );
}
export function Modal({
  wide = false,
  open,
  onOpenChange,
  title,
  description,
  children,
}: {
  wide?: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: React.ReactNode;
  description: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="overlay" />
        <Dialog.Content className="dialog" data-wide={wide}>
          <Dialog.Title>{title}</Dialog.Title>
          <Dialog.Description>{description}</Dialog.Description>
          <Dialog.Close className="dialog-close" aria-label="Close">
            <X size={18} />
          </Dialog.Close>
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
const features = tableFeatures({ columnVisibilityFeature, rowSortingFeature });
export type Column<T extends RowData> = ColumnDef<typeof features, T>;
export function DataTable<T extends RowData>({
  data,
  columns,
  label,
  rowId,
  sorting,
  onSortingChange,
  columnVisibility,
  onColumnVisibilityChange,
}: {
  data: T[];
  columns: Column<T>[];
  label: string;
  rowId: (row: T) => string;
  sorting?: SortingState;
  onSortingChange?: OnChangeFn<SortingState>;
  columnVisibility?: ColumnVisibilityState;
  onColumnVisibilityChange?: OnChangeFn<ColumnVisibilityState>;
}) {
  const table = useTable({ features, data, columns, getRowId: rowId, manualSorting:true, enableSorting:!!onSortingChange, enableMultiSort:false, sortDescFirst:false, state:{sorting:sorting ?? [], ...(columnVisibility !== undefined ? {columnVisibility} : {})}, onSortingChange, ...(onColumnVisibilityChange ? {onColumnVisibilityChange} : {}) });
  const [configuring, setConfiguring] = useState(false);
  const leafColumns = table.getAllLeafColumns();
  const hideable = leafColumns.filter(column => column.getCanHide());
  return (
    <>
    {hideable.length > 1 && <div className="table-controls">
      <button type="button" className="secondary" aria-label={`Choose columns for ${label}`} onClick={() => setConfiguring(true)}>Columns</button>
      {!table.getIsAllColumnsVisible() && <button type="button" className="secondary" aria-label={`Show all columns for ${label}`} onClick={() => table.resetColumnVisibility(true)}>Show all columns</button>}
    </div>}
    <Modal open={configuring} onOpenChange={setConfiguring} title={`Columns — ${label}`} description="Choose which columns appear on this screen. This changes presentation only.">
      {configuring && <ActionForm fields={[{
        name:"columns", label:"Visible columns", type:"checks",
        defaultValue:hideable.filter(column => column.getIsVisible()).map(column => column.id).join(","),
        options:hideable.map(column => ({value:column.id,label:typeof column.columnDef.header === "string" ? column.columnDef.header : column.id})),
        schema:z.string().refine(value => leafColumns.some(column => !column.getCanHide()) || hideable.some(column => value.split(",").includes(column.id)), "Keep at least one column visible."),
      }]} submitLabel="Apply columns" onSubmit={({columns: selected}) => {
        const visible = new Set(selected.split(","));
        table.setColumnVisibility(Object.fromEntries(leafColumns.map(column => [column.id, !column.getCanHide() || visible.has(column.id)])));
        setConfiguring(false);
      }}/>}
    </Modal>
    <div className="table-wrap">
      <table aria-label={label}>
        <thead>
          {table.getHeaderGroups().map((group) => (
            <tr key={group.id}>
              {group.headers.map((header) => (
                <th key={header.id} scope="col" aria-sort={header.column.getCanSort() ? (header.column.getIsSorted() === "asc" ? "ascending" : header.column.getIsSorted() === "desc" ? "descending" : "none") : undefined}>
                  {header.isPlaceholder ? null : header.column.getCanSort() ? <button type="button" className="table-sort" onClick={header.column.getToggleSortingHandler()}>
                    {flexRender(header.column.columnDef.header,header.getContext())}
                    <span aria-hidden="true">{header.column.getIsSorted() === "asc" ? " ↑" : header.column.getIsSorted() === "desc" ? " ↓" : " ↕"}</span>
                  </button> : flexRender(header.column.columnDef.header,header.getContext())}
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.map((row) => (
            <tr key={row.id}>
              {row.getVisibleCells().map((cell) => (
                <td key={cell.id}>
                  {flexRender(cell.column.columnDef.cell, cell.getContext())}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
    </>
  );
}
export type FormField = {
  name: string;
  label: string;
  type?: "tag-labels" | "tag-names" | "text" | "email" | "password" | "textarea" | "select" | "checks" | "datetime-local" | "number";
  when?: { name: string; is: string[] } | { name: string; is: string[] }[];
  description?: string;
  defaultValue?: string;
  options?: { value: string; label: string }[];
  schema?: z.ZodType<string, string>;
  autoComplete?: string;
  placeholder?: string;
};
// Shared declarative form: pages provide fields and a submit action, not bespoke form markup.
export function ActionForm({
  fields,
  onSubmit,
  submitLabel,
  pending = false,
  error,
}: {
  fields: FormField[];
  onSubmit: (values: Record<string, string>) => Promise<unknown> | void;
  submitLabel: string;
  pending?: boolean;
  error?: unknown;
}) {
  const id = useId();
  const form = useForm({
    defaultValues: Object.fromEntries(
      fields.map((f) => [f.name, f.defaultValue ?? ""]),
    ),
    onSubmit: async ({ value }) => {
      try {
        await onSubmit(value);
      } catch {
        /* Mutation errors render through ErrorNote; retain input for retry. */
      }
    },
  });
  return (
    <form
      className="action-form"
      onSubmit={(e) => {
        e.preventDefault();
        e.stopPropagation();
        void form.handleSubmit();
      }}
    >
      <ErrorNote error={error} />
      {<form.Subscribe selector={(s) => s.values}>{(values) => fields.filter((f) => !f.when || (Array.isArray(f.when) ? f.when : [f.when]).every(condition => condition.is.includes(values[condition.name]))).map((f) => (
        <form.Field
          key={f.name}
          name={f.name}
          validators={{
            onChange: f.schema ?? z.string().min(1, "This field is required."),
          }}
        >
          {(field) => {
            const errorText = field.state.meta.errors
              .map((e) => (typeof e === "string" ? e : e?.message))
              .filter(Boolean)
              .join(" ");
            const common = {
              id: `${id}-${f.name}`,
              name: f.name,
              value: field.state.value,
              onBlur: field.handleBlur,
              "aria-invalid": !!errorText,
              "aria-describedby": `${id}-${f.name}-help`,
            };
            return (
              <div className="field">
                <label htmlFor={common.id}>{f.label}</label>
                {f.type === "tag-labels" || f.type === "tag-names" ? (
                  <TagFields id={common.id} label={f.label} namesOnly={f.type === "tag-names"} value={field.state.value} onChange={field.handleChange} />
                ) : f.type === "select" ? (
                  <select
                    {...common}
                    onChange={(e) => field.handleChange(e.target.value)}
                  >
                    {f.options?.map((o) => (
                      <option key={o.value} value={o.value}>
                        {o.label}
                      </option>
                    ))}
                  </select>
                ) : f.type === "checks" ? (
                  <fieldset id={common.id} className="check-options">
                    <legend className="sr-only">{f.label}</legend>
                    {f.options?.map((o) => (
                      <label key={o.value}>
                        <input
                          type="checkbox"
                          checked={field.state.value
                            .split(",")
                            .includes(o.value)}
                          onChange={(e) => {
                            const values = field.state.value
                              .split(",")
                              .filter(Boolean);
                            field.handleChange(
                              (e.target.checked
                                ? [...values, o.value]
                                : values.filter((v) => v !== o.value)
                              ).join(","),
                            );
                          }}
                        />
                        {o.label}
                      </label>
                    ))}
                  </fieldset>
                ) : f.type === "textarea" ? (
                  <textarea
                    {...common}
                    rows={4}
                    autoComplete="off"
                    onChange={(e) => field.handleChange(e.target.value)}
                  />
                ) : (
                  <input
                    {...common}
                    type={f.type ?? "text"}
                    placeholder={f.placeholder}
                    autoComplete={f.autoComplete}
                    onChange={(e) => field.handleChange(e.target.value)}
                  />
                )}
                <span id={`${id}-${f.name}-help`}>
                  {f.description && <small>{f.description}</small>}
                  {errorText && <small role="alert">{errorText}</small>}
                </span>
              </div>
            );
          }}
        </form.Field>
      ))}</form.Subscribe>}
      <form.Subscribe selector={(s) => [s.canSubmit, s.isSubmitting]}>
        {([canSubmit, isSubmitting]) => (
          <button
            className="primary full"
            disabled={!canSubmit || isSubmitting || pending}
          >
            {pending || isSubmitting ? "Working…" : submitLabel}
          </button>
        )}
      </form.Subscribe>
    </form>
  );
}

export function MetricPlot({points,start,end,label,unit}:{points:{timestamp:string;value:number}[];start:string;end:string;label:string;unit:string}) {
  const from=Date.parse(start),span=Math.max(1,Date.parse(end)-from),maximum=Math.max(1,...points.map(p=>p.value));
  return <figure className="metric-plot"><svg viewBox="0 0 900 200" role="img" aria-label={`${label}; ${points.length} reported samples in ${unit}. Exact values follow in the table.`}>
    <line x1="55" y1="15" x2="55" y2="170"/><line x1="55" y1="170" x2="875" y2="170"/>
    <text x="4" y="22">{maximum.toLocaleString(undefined,{maximumFractionDigits:2})}</text><text x="35" y="170">0</text>
    {points.map(p=><circle key={p.timestamp} cx={55+(Date.parse(p.timestamp)-from)/span*820} cy={170-p.value/maximum*150} r="2"><title>{p.timestamp}: {p.value} {unit}</title></circle>)}
    <text x="55" y="195">{start}</text><text x="875" y="195" textAnchor="end">{end}</text>
  </svg><figcaption>Reported samples, UTC. Gaps are left empty; measurements are not interpolated.</figcaption></figure>;
}

function TagFields({id,label,namesOnly,value,onChange}:{id:string;label:string;namesOnly:boolean;value:string;onChange:(value:string)=>void}) {
  const rows: string[][] = JSON.parse(value || "[]");
  const save = (next:string[][]) => onChange(JSON.stringify(next));
  return <fieldset id={id} className="tag-fields"><legend>{label}</legend>
    {rows.map((row,index)=><div className="tag-field-row" key={index}>
      <textarea rows={1} aria-label={`Tag ${index+1} ${namesOnly ? "name" : "key"}`} value={row[0]} onChange={event=>save(rows.map((r,i)=>i===index?[event.target.value,r[1]]:r))}/>
      {!namesOnly && <textarea rows={1} aria-label={`Tag ${index+1} value`} value={row[1]} onChange={event=>save(rows.map((r,i)=>i===index?[r[0],event.target.value]:r))}/>}
      <button type="button" className="secondary" aria-label={`Remove tag ${index+1}`} onClick={()=>save(rows.filter((_,i)=>i!==index))}>Remove</button>
    </div>)}
    {!rows.length && <p>No tags. Saving this set removes existing editable tags.</p>}
    <button type="button" className="secondary" disabled={rows.length>=100} onClick={()=>save([...rows,["",""]])}>Add tag</button>
  </fieldset>;
}
