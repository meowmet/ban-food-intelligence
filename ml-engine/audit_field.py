"""Audit frozen predictions before claiming prospective field accuracy; standard library only.

python ml-engine/audit_field.py snapshot.jsonl --origins ml-engine/field_origins.json --output report.json

Unverified/demo data are never included in field metrics. This program does not retrain,
rewrite forecasts, infer data origin from source='db', or infer independent carcasses from
six correlated part rows. Source attestations and physical observation times must be supplied
from verified field records; database booking timestamps alone cannot prove those facts.
A butchery forecast can only precede the weighing if it was frozen beforehand
(forecast_source 'butchery_forecast'); one stored with the operation never does.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
from datetime import datetime
import json
import hashlib
import math
from pathlib import Path

MODEL = 'ali_ridge'
BASELINE = 'baseline_hist_mean'


def timestamp(value):
    if not value:
        return None
    parsed = datetime.fromisoformat(value.replace('Z', '+00:00'))
    if parsed.tzinfo is None:
        raise ValueError('timestamps must include timezone')
    return parsed


def number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)


def paired_rows(rows):
    groups = defaultdict(dict)
    for row in rows:
        key = (row['reference_type'], row['reference'], row['target'])
        if row['model'] in groups[key]:
            raise ValueError(f'duplicate forecast: {key} / {row["model"]}; select a frozen version explicitly')
        groups[key][row['model']] = row
    paired, exclusions = [], Counter()
    for key, versions in sorted(groups.items()):
        if MODEL not in versions or BASELINE not in versions:
            exclusions['missing_model_or_baseline'] += 1
            continue
        model, baseline = versions[MODEL], versions[BASELINE]
        values = [model.get('actual'), model.get('predicted'), baseline.get('actual'), baseline.get('predicted')]
        if not all(number(v) and v >= 0 for v in values):
            exclusions['missing_or_invalid_values'] += 1
            continue
        if abs(model['actual'] - baseline['actual']) > 0.0001:
            exclusions['actual_disagreement'] += 1
            continue
        if any(r.get('stored_actual') is not None and (not number(r['stored_actual']) or
                abs(r['stored_actual'] - r['actual']) > 0.0001) for r in (model, baseline)):
            exclusions['stale_stored_actual'] += 1
            continue
        # A forecast frozen for another input weight than the one cut forecasts a different carcass.
        if any(r.get('forecast_input_kg') is not None and (not number(r['forecast_input_kg']) or
                not number(r.get('outcome_input_kg')) or abs(r['forecast_input_kg'] - r['outcome_input_kg']) > 0.0005)
               for r in (model, baseline)):
            exclusions['forecast_input_differs'] += 1
            continue
        paired.append((model, baseline))
    return paired, dict(exclusions)


def field_exclusion(pair, origins):
    model, baseline = pair
    key = f'{model["reference_type"]}:{model["reference"]}'
    provenance = origins.get(key, {})
    if provenance.get('origin') != 'field_verified':
        return 'not_verified_field_data'
    if not provenance.get('source_ref') or not provenance.get('verified_by'):
        return 'missing_source_attestation'
    if provenance.get('held_out_from_training') is not True:
        return 'training_independence_unverified'
    try:
        observed = timestamp(provenance.get('outcome_observed_at'))
        booked = timestamp(model.get('outcome_booked_at'))
        forecasts = [timestamp(row.get('predicted_at')) for row in pair]
    except (TypeError, ValueError):
        return 'invalid_timestamps'
    if not observed or not booked or any(t is None for t in forecasts):
        return 'physical_observation_or_forecast_time_missing'
    if observed > booked:
        return 'observation_after_booking'
    if any(t >= observed for t in forecasts):
        return 'forecast_not_before_observation'
    return None


def metrics(pairs):
    if not pairs:
        return None
    actuals = [m['actual'] for m, _ in pairs]
    result = {'rows': len(pairs), 'independent_operations': len({(m['reference_type'], m['reference']) for m, _ in pairs})}
    for label, index in [('model', 0), ('baseline', 1)]:
        errors = [pair[index]['predicted'] - pair[0]['actual'] for pair in pairs]
        mae = sum(abs(e) for e in errors) / len(errors)
        result[label] = {'mae': round(mae, 6),
                         'rmse': round(math.sqrt(sum(e * e for e in errors) / len(errors)), 6),
                         'bias_predicted_minus_actual': round(sum(errors) / len(errors), 6),
                         'wape_pct': round(100 * sum(abs(e) for e in errors) / sum(actuals), 6) if sum(actuals) else None}
    result['model_minus_baseline_mae'] = round(result['model']['mae'] - result['baseline']['mae'], 6)
    # Coverage is descriptive only; its denominator is not the number of independent carcasses.
    intervals = [m for m, _ in pairs if m.get('level') == 0.8 and number(m.get('low')) and number(m.get('high')) and m['low'] <= m['high']]
    result['interval_80_rows'] = len(intervals)
    result['interval_80_coverage_pct'] = round(100 * sum(m['low'] <= m['actual'] <= m['high'] for m in intervals) / len(intervals), 6) if intervals else None
    return result


def summarize(pairs):
    groups = defaultdict(list)
    for pair in pairs:
        model = pair[0]
        if model['reference_type'] == 'production_order':
            key = f'production/{model["target"]}'
        else:
            key = 'butchery/recorded_parts_kg'
        groups[key].append(pair)
    return {key: metrics(group) for key, group in sorted(groups.items())}


def audit(rows, origins):
    pairs, invalid = paired_rows(rows)
    versions = {m['version'] for m, _ in pairs}
    if len(versions) > 1:
        raise ValueError('mixed model versions; audit each frozen version separately')
    accepted, excluded = [], Counter()
    per_operation = {}
    for pair in pairs:
        model, baseline = pair
        reason = field_exclusion(pair, origins)
        if reason:
            excluded[reason] += 1
        else:
            accepted.append(pair)
        key = f'{model["reference_type"]}:{model["reference"]}'
        item = per_operation.setdefault(key, {'origin': origins.get(key, {}).get('origin', 'unknown'), 'targets': [],
            'booking_time_is_only_an_operational_proxy': True})
        try:
            booked = timestamp(model.get('outcome_booked_at'))
            before = bool(booked and all(timestamp(r.get('predicted_at')) and timestamp(r['predicted_at']) < booked for r in pair))
        except (ValueError, TypeError):
            before = False
        item['targets'].append({'target': model['target'], 'model_version': model['version'],
            'forecast_source': model.get('forecast_source', 'unknown'),
            'both_forecasts_before_inventory_booking': before,
            'model_predicted_at': model.get('predicted_at'), 'outcome_booked_at': model.get('outcome_booked_at'),
            'field_exclusion': reason})
    return {'status': 'NO_VERIFIED_FIELD_PAIRS' if not accepted else 'DESCRIPTIVE_FIELD_METRICS_ONLY',
        'model_versions': sorted(versions),
        'field_metrics': summarize(accepted), 'field_exclusion_target_counts': dict(excluded),
        'invalid_pair_counts': invalid, 'operations': per_operation,
        'demo_diagnostic_metrics_NOT_FIELD': summarize([p for p in pairs if origins.get(f'{p[0]["reference_type"]}:{p[0]["reference"]}', {}).get('origin') in ('demo_seed', 'demo_manual')]),
        'notes': ['No claim of field accuracy or business benefit is made from demo rows.',
                  'Six part rows from one carcass are one independent operation, not six field cases.',
                  'Inventory booking time does not establish when a human first knew the outcome.',
                  'No statistical significance, real-data sample-size guarantee or deployment approval is inferred.']}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('snapshot', type=Path)
    parser.add_argument('--origins', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    rows = [json.loads(line) for line in args.snapshot.read_text(encoding='utf-8-sig').splitlines() if line.strip()]
    report = audit(rows, json.loads(args.origins.read_text(encoding='utf-8-sig')))
    report['snapshot_sha256'] = hashlib.sha256(args.snapshot.read_bytes()).hexdigest()
    report['origins_sha256'] = hashlib.sha256(args.origins.read_bytes()).hexdigest()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2, ensure_ascii=False) + '\n', encoding='utf-8')
    print(json.dumps({'status': report['status'], 'field_metrics': report['field_metrics'],
                      'demo_diagnostic_metrics_NOT_FIELD': report['demo_diagnostic_metrics_NOT_FIELD']}, indent=2))


if __name__ == '__main__':
    main()
