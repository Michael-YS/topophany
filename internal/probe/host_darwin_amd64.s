#include "textflag.h"

TEXT ·hostSelfPC(SB),NOSPLIT,$0-8
	MOVQ $topo_mach_host_self_trampoline(SB), AX
	MOVQ AX, ret+0(FP)
	RET

TEXT topo_mach_host_self_trampoline(SB),NOSPLIT,$0-0
	JMP topo_mach_host_self(SB)

TEXT ·hostStatisticsPC(SB),NOSPLIT,$0-8
	MOVQ $topo_host_statistics_trampoline(SB), AX
	MOVQ AX, ret+0(FP)
	RET

TEXT topo_host_statistics_trampoline(SB),NOSPLIT,$0-0
	JMP topo_host_statistics(SB)

TEXT ·hostStatistics64PC(SB),NOSPLIT,$0-8
	MOVQ $topo_host_statistics64_trampoline(SB), AX
	MOVQ AX, ret+0(FP)
	RET

TEXT topo_host_statistics64_trampoline(SB),NOSPLIT,$0-0
	JMP topo_host_statistics64(SB)

TEXT ·hostSysctlPC(SB),NOSPLIT,$0-8
	MOVQ $topo_sysctl_trampoline(SB), AX
	MOVQ AX, ret+0(FP)
	RET

TEXT topo_sysctl_trampoline(SB),NOSPLIT,$0-0
	JMP topo_sysctl(SB)

TEXT ·hostProcInfoPC(SB),NOSPLIT,$0-8
	MOVQ $topo_proc_pidinfo_trampoline(SB), AX
	MOVQ AX, ret+0(FP)
	RET

TEXT topo_proc_pidinfo_trampoline(SB),NOSPLIT,$0-0
	JMP topo_proc_pidinfo(SB)

