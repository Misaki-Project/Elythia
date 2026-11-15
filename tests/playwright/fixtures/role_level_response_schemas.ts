/*
 * SPDX-FileCopyrightText: Elythia-Network
 * SPDX-License-Identifier: AGPL-3.0-only
 */

/**
 * RoleLevel is a plugin, so its response declarations are not part of the
 * server's built-in `elythia_apidoc.json`. Keep the Playwright contract here:
 * it describes the values returned by plugins/rolelevel and is consumed by
 * the same response validator as the built-in Elythia API schemas.
 *
 * The endpoint keys are checked against every registered RoleLevel route in
 * /api.json. Adding or removing a route therefore requires an explicit
 * contract update; there is no pending/ignore list.
 */

type Schema = Record<string, unknown>;

const string: Schema = { type: 'string' };
const integer: Schema = { type: 'integer' };
const number: Schema = { type: 'number' };
const boolean: Schema = { type: 'boolean' };
const nullable = (schema: Schema): Schema => ({ anyOf: [schema, { type: 'null' }] });
const array = (items: Schema): Schema => ({ type: 'array', items });
const object = (properties: Record<string, Schema>, required = Object.keys(properties)): Schema => ({
  type: 'object',
  properties,
  required,
  additionalProperties: false,
});

const components: Record<string, Schema> = {
  RoleLevelCurve: object({
    type: { type: 'string', enum: ['const', 'linear', 'exponential'] },
    levelUps: integer,
    base: integer,
    additional: integer,
    exponential: number,
  }, ['type', 'levelUps', 'base', 'additional']),
  RoleLevelPolicyRange: object({
    type: { type: 'string', enum: ['base', 'const', 'multiplier'] },
    key: string,
    start: integer,
    end: integer,
    value: {
      anyOf: [
        { type: 'boolean' },
        { type: 'number' },
        { type: 'string' },
        { type: 'array', items: { type: 'string' } },
      ],
    },
    base: number,
    additional: number,
  }, ['type', 'start', 'end']),
  RoleLevelConfig: object({
    roleId: string,
    baseLevel: integer,
    experienceCurve: nullable(array({ $ref: '#/components/schemas/RoleLevelCurve' })),
    policyRanges: array({ $ref: '#/components/schemas/RoleLevelPolicyRange' }),
    revision: integer,
    updatedBy: string,
    createdAt: string,
    updatedAt: string,
  }),
  RoleLevelExperience: object({
    currentLevel: integer,
    currentLevelExp: integer,
    nextLevelExp: nullable(integer),
    totalExp: integer,
    minLevel: integer,
    maxLevel: integer,
    progressionStage: integer,
  }),
  RoleLevelConfigAuditState: object({
    roleId: string,
    baseLevel: integer,
    experienceCurve: nullable(array({ $ref: '#/components/schemas/RoleLevelCurve' })),
    policyRanges: array({ $ref: '#/components/schemas/RoleLevelPolicyRange' }),
    revision: integer,
    updatedBy: string,
  }),
  RoleLevelExperienceAuditState: object({ experience: integer }),
  RoleLevelPrunedRowsAuditState: object({
    deleted: integer,
    rows: array(object({ assignmentId: string, userId: string, experience: integer })),
  }),
  RoleLevelPrunedCountAuditState: object({ deleted: integer }),
  RoleLevelAuditEntry: object({
    id: string,
    actorId: string,
    operation: string,
    roleId: nullable(string),
    userId: nullable(string),
    assignmentId: nullable(string),
    beforeExperience: nullable(integer),
    afterExperience: nullable(integer),
    before: { oneOf: [
      { $ref: '#/components/schemas/RoleLevelConfigAuditState' },
      { $ref: '#/components/schemas/RoleLevelExperienceAuditState' },
      { $ref: '#/components/schemas/RoleLevelPrunedRowsAuditState' },
    ] },
    after: { oneOf: [
      { $ref: '#/components/schemas/RoleLevelConfigAuditState' },
      { $ref: '#/components/schemas/RoleLevelExperienceAuditState' },
      { $ref: '#/components/schemas/RoleLevelPrunedCountAuditState' },
    ] },
    note: nullable(string),
    createdAt: string,
  }, ['id', 'actorId', 'operation', 'roleId', 'userId', 'assignmentId', 'beforeExperience', 'afterExperience', 'note', 'createdAt']),
  RoleLevelOrphanReport: object({
    tracked: integer,
    orphans: integer,
    prunable: integer,
    orphanAssignmentIds: array(string),
  }),
};

const configRef: Schema = { $ref: '#/components/schemas/RoleLevelConfig' };
const experienceRef: Schema = { $ref: '#/components/schemas/RoleLevelExperience' };
const auditEntryRef: Schema = { $ref: '#/components/schemas/RoleLevelAuditEntry' };
const orphanReportRef: Schema = { $ref: '#/components/schemas/RoleLevelOrphanReport' };
const keyedValues = (value: Schema): Schema => ({ type: 'object', additionalProperties: value });

/** Explicit 200 JSON response schemas keyed by the exact /api.json endpoint. */
export const ROLE_LEVEL_RESPONSE_SCHEMAS = {
  components,
  endpoints: {
    'plugin/role-level/admin/roles/list': object({
      roles: array(configRef),
      memberCounts: keyedValues(integer),
      memberCountsTruncated: keyedValues(boolean),
    }),
    'plugin/role-level/admin/roles/show': object({
      role: configRef,
      memberCount: integer,
      membersTruncated: boolean,
    }),
    'plugin/role-level/admin/roles/update': object({ role: configRef }),
    'plugin/role-level/admin/roles/delete': object({ roleId: string, deleted: { const: true } }),
    'plugin/role-level/admin/users/show': object({
      userId: string,
      roles: array(object({ roleId: string, assignmentId: string, experience: integer, level: experienceRef })),
      audit: array(auditEntryRef),
      operations: array(object({
        idempotencyKey: string,
        roleId: string,
        mode: { type: 'string', enum: ['set', 'add', 'multiplier'] },
        operand: number,
        status: { type: 'string', enum: ['pending', 'assigning', 'applying', 'completed', 'failed'] },
        lastError: string,
      })),
    }),
    'plugin/role-level/admin/change-exp': object({
      assignmentId: string,
      experience: integer,
      status: { type: 'string', enum: ['completed'] },
      resumed: boolean,
    }),
    'plugin/role-level/admin/audit': object({ entries: array(auditEntryRef) }),
    'plugin/role-level/admin/orphans': object({ roles: keyedValues(orphanReportRef) }),
    'plugin/role-level/admin/reconcile': object({
      mode: { type: 'string', enum: ['all', 'resume-operations', 'reconcile-orphans', 'prune-orphans'] },
      result: object({ steps: array(object({
        mode: { type: 'string', enum: ['resume-operations', 'reconcile-orphans', 'prune-orphans'] },
        result: { anyOf: [
          { type: 'null' },
          orphanReportRef,
          object({ pruned: integer }),
        ] },
      })) }),
    }),
    'plugin/role-level/roles/users': object({
      roleId: string,
      total: integer,
      truncated: boolean,
      members: array(object({
        userId: string,
        username: string,
        assignmentId: string,
        experience: integer,
        level: experienceRef,
      })),
    }),
    'plugin/role-level/users/show': object({
      userId: string,
      roles: array(object({ roleId: string, experience: integer, level: experienceRef })),
    }),
    'plugin/role-level/users/profile-settings': object({
      userId: string,
      roles: array(object({ roleId: string, experience: integer, level: experienceRef, hidden: boolean })),
    }),
    'plugin/role-level/users/profile-hide': object({ roleId: string, hidden: boolean }),
  } satisfies Record<string, Schema>,
};
