// Code generated - EDITING IS FUTILE. DO NOT EDIT.
//
// Generated by:
//     public/app/plugins/gen.go
// Using jennies:
//     TSTypesJenny
//     PluginTsTypesJenny
//
// Run 'make gen-cue' from repository root to regenerate.

import * as ui from '@grafana/schema';

export const pluginVersion = "11.1.5";

export interface Options extends ui.OptionsWithLegend, ui.OptionsWithTooltip, ui.OptionsWithTimezones {
  /**
   * Controls value alignment on the timelines
   */
  alignValue?: ui.TimelineValueAlignment;
  /**
   * Merge equal consecutive values
   */
  mergeValues?: boolean;
  /**
   * Controls the row height
   */
  rowHeight: number;
  /**
   * Show timeline values on chart
   */
  showValue: ui.VisibilityMode;
}

export const defaultOptions: Partial<Options> = {
  alignValue: 'left',
  mergeValues: true,
  rowHeight: 0.9,
  showValue: ui.VisibilityMode.Auto,
};

export interface FieldConfig extends ui.HideableFieldConfig {
  fillOpacity?: number;
  lineWidth?: number;
}

export const defaultFieldConfig: Partial<FieldConfig> = {
  fillOpacity: 70,
  lineWidth: 0,
};
