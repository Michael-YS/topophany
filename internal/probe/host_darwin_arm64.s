#include "textflag.h"

TEXT ·hostSelfPC(SB),NOSPLIT,$0-8
	MOVD $topo_mach_host_self_trampoline(SB), R0
	MOVD R0, ret+0(FP)
	RET

TEXT topo_mach_host_self_trampoline(SB),NOSPLIT,$0-0
	B topo_mach_host_self(SB)

TEXT ·hostStatisticsPC(SB),NOSPLIT,$0-8
	MOVD $topo_host_statistics_trampoline(SB), R0
	MOVD R0, ret+0(FP)
	RET

TEXT topo_host_statistics_trampoline(SB),NOSPLIT,$0-0
	B topo_host_statistics(SB)

TEXT ·hostStatistics64PC(SB),NOSPLIT,$0-8
	MOVD $topo_host_statistics64_trampoline(SB), R0
	MOVD R0, ret+0(FP)
	RET

TEXT topo_host_statistics64_trampoline(SB),NOSPLIT,$0-0
	B topo_host_statistics64(SB)

TEXT ·hostSysctlPC(SB),NOSPLIT,$0-8
	MOVD $topo_sysctl_trampoline(SB), R0
	MOVD R0, ret+0(FP)
	RET

TEXT topo_sysctl_trampoline(SB),NOSPLIT,$0-0
	B topo_sysctl(SB)

TEXT ·hostProcInfoPC(SB),NOSPLIT,$0-8
	MOVD $topo_proc_pidinfo_trampoline(SB), R0
	MOVD R0, ret+0(FP)
	RET

TEXT topo_proc_pidinfo_trampoline(SB),NOSPLIT,$0-0
	B topo_proc_pidinfo(SB)
