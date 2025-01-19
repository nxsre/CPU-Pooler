package types

const (
	//SharedPoolID is the constant prefix in the name of the CPU pool. It is used to signal that a CPU pool is of shared type
	// SharedPoolID 是 CPU 池名称中的常量前缀. 表示 CPU 池是共享类型的
	SharedPoolID = "shared"
	//ExclusivePoolID is the constant prefix in the name of the CPU pool. It is used to signal that a CPU pool is of exclusive type
	// ExclusivePoolID 是 CPU 池名称中的常量前缀. 表示 CPU 池是独占类型
	ExclusivePoolID = "exclusive"
	// CloudphonePoolID 是 cloudphone 专用资源池的前缀，满足 cloudphone 场景下 CPU/GPU/编码卡 分配绑定numa节点的需求
	CloudphonePoolID = "cloudphone"
	//DefaultPoolID is the constant prefix in the name of the CPU pool. It is used to signal that a CPU pool is of default type
	// DefaultPoolID 是 CPU 池名称中的常量前缀. 表示 CPU 池是默认类型
	DefaultPoolID = "default"
	//SingleThreadHTPolicy is the constant for the single threaded value of the HT policy pool attribute. Only the physical thread is allocated for exclusive requests when this value is set
	// SingleThreadHTPolicy 是 HT 策略池属性的单线程值的常量。设置该值时，只为独占请求分配物理线程
	SingleThreadHTPolicy = "singleThreaded"
	//MultiThreadHTPolicy is the constant for the multi threaded value of the HT policy pool attribute. All siblings are allocated together for exclusive requests when this value is set
	// MultiThreadHTPolicy 是 HT 策略池属性的多线程值的常量。设置此值时，所有兄弟一起分配用于独占请求
	MultiThreadHTPolicy = "multiThreaded"
)
