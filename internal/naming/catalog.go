package naming

// opsModules are the modules ops emits CloudWatch hooks for. ops.SupportedModules
// is the authority; naming cannot import ops, so the list is mirrored here.
var opsModules = []string{"perforce", "horde", "ddc", "ci", "deploy"}

// Catalog returns every physical AWS name Fabrica generates for an account and
// region, keyed by "<area>.<name>". Names that carry a version or build slug
// (AMI names, GameLift fleet and build names) appear as their prefix; the
// caller appends the suffix.
func Catalog(p Prefix, account, region string) map[string]string {
	c := catalogBuilder{p: p, account: account, region: region, out: make(map[string]string, 96)}
	c.perforce()
	c.lore()
	c.ddc()
	c.horde()
	c.workstation()
	c.ci()
	c.deploy()
	c.opsHooks()
	c.state()
	c.iam()
	c.export()
	c.ami()
	return c.out
}

type catalogBuilder struct {
	p       Prefix
	account string
	region  string
	out     map[string]string
}

func (c catalogBuilder) put(key, name string) {
	c.out[key] = name
}

// putModule records the standard EC2 module set: SG, instance, role, profile.
func (c catalogBuilder) putModule(module string) {
	c.put(module+".sg", c.p.SG(module))
	c.put(module+".instance", c.p.Instance(module))
	c.put(module+".role", c.p.Role(module))
	c.put(module+".profile", c.p.Profile(module))
}

func (c catalogBuilder) perforce() {
	c.putModule("perforce")
	c.put("perforce.backupPolicy", c.p.For("perforce", "backup", "s3"))
}

func (c catalogBuilder) lore() {
	c.putModule("lore")
	bucket := c.p.Bucket("lore-store", c.account, c.region)
	c.put("lore.storeBucket", bucket)
	c.put("lore.storeTableFragments", bucket+"-fragments")
	c.put("lore.storeTableMetadata", bucket+"-metadata")
	c.put("lore.storeTableMutable", bucket+"-mutable")
	c.put("lore.storeTableLocks", bucket+"-locks")
	c.put("lore.storeDynamoDBPolicy", c.p.For("lore", "store", "dynamodb"))
	c.put("lore.storeS3Policy", c.p.For("lore", "store", "s3"))

	costBucket := c.p.For("lore", "store")
	c.put("lore.costBucket", costBucket)
	c.put("lore.costStoreTableFragments", costBucket+"-fragments")
	c.put("lore.costStoreTableMetadata", costBucket+"-metadata")
	c.put("lore.costStoreTableMutable", costBucket+"-mutable")
	c.put("lore.costStoreTableLocks", costBucket+"-locks")
	c.put("lore.amiPrefix", c.p.For("lore"))
}

func (c catalogBuilder) ddc() {
	c.putModule("ddc")
	c.put("ddc.scyllaInstance", c.p.For("ddc", "scylla"))
	c.put("ddc.bucket", c.p.Bucket("ddc", c.account, c.region))
	c.put("ddc.s3Policy", c.p.For("ddc", "s3"))
	c.put("ddc.edgeInstance", c.p.For("ddc", "edge", c.region))
	c.put("ddc.costBucket", c.p.For("ddc"))
	c.put("ddc.amiPrefix", c.p.For("ddc"))
}

func (c catalogBuilder) horde() {
	c.putModule("horde")
	c.put("horde.serviceTokenPolicy", c.p.For("horde", "service", "token"))
	c.put("horde.ssmPath", c.p.ParamPath("horde"))
	c.put("horde.agentsSG", c.p.For("horde", "agents", "sg"))
	c.put("horde.agentsRole", c.p.For("horde", "agents", "role"))
	c.put("horde.agentsProfile", c.p.For("horde", "agents", "profile"))
	c.put("horde.agentsLaunchTemplate", c.p.For("horde", "agents", "lt"))
	c.put("horde.agentsASG", c.p.For("horde", "agents", "asg"))
	c.put("horde.agentsScaleOutAlarm", c.p.For("horde", "agents", "scale", "out"))
	c.put("horde.agentsScaleInAlarm", c.p.For("horde", "agents", "scale", "in"))
	c.put("horde.agentsScaleOutPolicy", c.p.For("horde", "agents", "scale", "out", "policy"))
	c.put("horde.agentsScaleInPolicy", c.p.For("horde", "agents", "scale", "in", "policy"))
	c.put("horde.agentNameTag", c.p.For("horde", "agent"))
	c.put("horde.amiPrefix", c.p.For("horde"))
}

func (c catalogBuilder) workstation() {
	c.putModule("workstation")
	c.put("workstation.dcvLicensePolicy", c.p.For("workstation", "dcv", "license"))
	c.put("workstation.dcvPasswordPolicy", c.p.For("workstation", "dcv", "password"))
	c.put("workstation.ssmPath", c.p.ParamPath("workstation"))
	c.put("workstation.amiName", c.p.For("workstation", "dcv"))
}

func (c catalogBuilder) ci() {
	c.put("ci.project", c.p.For("ci"))
	c.put("ci.pipeline", c.p.For("ci", "pipeline"))
	c.put("ci.role", c.p.For("ci", "codebuild"))
	c.put("ci.sg", c.p.SG("ci"))
	c.put("ci.hordeTokenPolicy", c.p.For("ci", "horde", "token"))
	c.put("ci.inlinePolicy", c.p.For("ci", "inline"))
}

func (c catalogBuilder) deploy() {
	c.put("deploy.role", c.p.For("deploy", "gamelift"))
	c.put("deploy.alias", c.p.For("deploy"))
	c.put("deploy.fleetPrefix", c.p.For("fleet"))
	c.put("deploy.buildPrefix", c.p.For("build"))
	c.put("deploy.s3Policy", c.p.For("deploy", "s3", "read"))
}

func (c catalogBuilder) opsHooks() {
	for _, m := range opsModules {
		c.put("ops."+m+".alarm", c.p.For(m, "unhealthy"))
		c.put("ops."+m+".logGroup", c.p.LogGroup(m))
	}
}

func (c catalogBuilder) state() {
	c.put("state.bucket", c.p.StateBucket(c.account))
	c.put("state.table", c.p.StateTable())
	c.put("state.lockID", c.p.StateLockID(c.account))
}

func (c catalogBuilder) iam() {
	c.put("iam.ssmOutputPolicy", c.p.For("ssm", "output"))
	c.put("iam.ssmLogGroup", c.p.LogGroup("ssm"))
}

// export holds the fallback names internal/export generates when a state
// record omits them. They are part of the physical surface and must follow
// the prefix like every other name.
func (c catalogBuilder) export() {
	c.put("export.deployFleet", c.p.For("deploy", "fleet"))
	c.put("export.deployBuild", c.p.For("deploy", "build"))
	c.put("export.deployAlias", c.p.For("deploy", "alias"))
	c.put("export.hordeScalingPolicy", c.p.For("horde", "agents", "scaling", "policy"))
}

func (c catalogBuilder) ami() {
	c.put("amissm.fallbackName", c.p.For("ami"))
}
