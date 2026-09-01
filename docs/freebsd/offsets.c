/* Offset verification for FreeBSD kernel export structs.
 * Transcribed verbatim from freebsd-src:
 *   sys/sys/socketvar.h (xsocket), sys/netinet/in_pcb.h (xinpcb/xinpgen/in_conninfo),
 *   sys/netinet/tcp_var.h (xtcpcb), sys/sys/file.h (xfile)
 * FreeBSD amd64 uses the same SysV ABI layout rules as Linux x86-64.
 */
#include <stdio.h>
#include <stdint.h>
#include <stddef.h>

typedef uint64_t ksize_t;
typedef uint64_t kvaddr_t;

struct xsockbuf {
	uint32_t sb_cc, sb_hiwat, sb_mbcnt, sb_mcnt, sb_ccnt, sb_mbmax;
	int32_t sb_lowat, sb_timeo;
	int16_t sb_flags;
};

struct xsocket {
	ksize_t  xso_len;
	kvaddr_t xso_so;
	kvaddr_t so_pcb;
	uint64_t so_oobmark;
	/* <=13.4: int64_t so_spare64[8]; 14.2+/15: so_splice_so + so_spare64[7] (same 64 bytes) */
	kvaddr_t so_splice_so;
	int64_t  so_spare64[7];
	int32_t  xso_protocol, xso_family;
	uint32_t so_qlen, so_incqlen, so_qlimit;
	int32_t  so_pgid; /* pid_t */
	uint32_t so_uid; /* uid_t */
	/* 15: so_fibnum + so_spare32[7]; <=14.2: so_spare32[8] (same 32 bytes) */
	int32_t  so_fibnum;
	int32_t  so_spare32[7];
	int16_t  so_type, so_options, so_linger, so_state, so_timeo;
	uint16_t so_error;
	struct xsockbuf so_rcv, so_snd;
};

/* <=14: old in_endpoints */
struct in_addr_4in6 { uint8_t ia46_pad12[12]; struct { uint32_t s_addr; } ia46_addr4; };
union in_dependaddr14 { struct in_addr_4in6 id46_addr; uint8_t id6_addr[16]; };
struct in_endpoints14 {
	uint16_t ie_fport, ie_lport;
	union in_dependaddr14 ie_dependfaddr, ie_dependladdr;
	uint32_t ie6_zoneid;
};
struct in_conninfo14 { uint8_t inc_flags, inc_len; uint16_t inc_fibnum; struct in_endpoints14 inc_ie; };

/* 15: new in_endpoints */
union in_dependaddr15 {
	struct { uint32_t __pad[3]; struct { uint32_t s_addr; } id4_addr; };
	uint8_t id6_addr[16];
};
struct in_endpoints15 {
	uint16_t ie_fport, ie_lport;
	union in_dependaddr15 ie_dependfaddr, ie_dependladdr;
	uint32_t ie6_zoneid;
};
struct in_conninfo15 { uint8_t inc_flags, inc_len; uint16_t inc_fibnum; struct in_endpoints15 inc_ie; };

struct xinpcb14 {
	ksize_t xi_len;
	struct xsocket xi_socket;
	struct in_conninfo14 inp_inc;
	uint64_t inp_gencnt;
	kvaddr_t inp_ppcb;        /* 15: part of inp_spare64[5] */
	int64_t  inp_spare64[4];  /* 15: [5] */
	uint32_t inp_flow, inp_flowid, inp_flowtype;
	int32_t  inp_flags, inp_flags2;
	int32_t  inp_rss_listen_bucket; /* 15: inp_unused */
	int32_t  in6p_cksum;
	int32_t  inp_spare32[4];
	uint16_t in6p_hops;
	uint8_t  inp_ip_tos;
	int8_t   pad8;
	uint8_t  inp_vflag, inp_ip_ttl, inp_ip_p, inp_ip_minttl;
	int8_t   inp_spare8[4];
} __attribute__((aligned(8)));

struct xinpcb15 {
	ksize_t xi_len;
	struct xsocket xi_socket;
	struct in_conninfo15 inp_inc;
	uint64_t inp_gencnt;
	int64_t  inp_spare64[5];
	uint32_t inp_flow, inp_flowid, inp_flowtype;
	int32_t  inp_flags, inp_flags2;
	uint32_t inp_unused;
	int32_t  in6p_cksum;
	int32_t  inp_spare32[4];
	uint16_t in6p_hops;
	uint8_t  inp_ip_tos;
	int8_t   pad8;
	uint8_t  inp_vflag, inp_ip_ttl, inp_ip_p, inp_ip_minttl;
	int8_t   inp_spare8[4];
} __attribute__((aligned(8)));

struct xinpgen {
	ksize_t xig_len;
	uint32_t xig_count, _xig_spare32;
	uint64_t xig_gen, xig_sogen;
	uint64_t _xig_spare64[4];
} __attribute__((aligned(8)));

/* xtcpcb: size is what matters. 12.4 tail = 18 int32 + spare32[26];
 * 13.4/14.2/15.0 tails all total the same 744. */
struct xtcpcb {
	ksize_t xt_len;
	struct xinpcb14 xt_inp;
	char xt_stack[32];  /* TCP_FUNCTION_NAME_LEN_MAX */
	char xt_logid[64];  /* TCP_LOG_ID_LEN */
	char xt_cc[16];     /* TCP_CA_NAME_MAX */
	int64_t spare64[6];
	/* 12.4: 18 int32 (72) + spare32[26] (104) = 176 */
	int32_t t_state; uint32_t t_flags; int32_t t_sndzerowin, t_sndrexmitpack, t_rcvoopack,
		t_rcvtime, tt_rexmt, tt_persist, tt_keep, tt_2msl, tt_delack, t_logstate;
	uint32_t t_snd_cwnd, t_snd_ssthresh, t_maxseg, t_rcv_wnd, t_snd_wnd, xt_ecn;
	int32_t spare32[26];
} __attribute__((aligned(8)));

struct xfile {
	ksize_t xf_size;
	int32_t xf_pid;   /* pid_t */
	uint32_t xf_uid;  /* uid_t */
	int32_t xf_fd, _xf_int_pad1;
	kvaddr_t xf_file;
	int16_t xf_type, _xf_short_pad1;
	int32_t xf_count, xf_msgcount, _xf_int_pad2;
	int64_t xf_offset; /* off_t */
	kvaddr_t xf_data, xf_vnode;
	uint32_t xf_flag;
	int32_t _xf_int_pad3;
	int64_t _xf_int64_pad[6];
};

int main(void) {
	printf("sizeof(xsocket)          = %zu\n", sizeof(struct xsocket));
	printf("sizeof(in_conninfo<=14)  = %zu\n", sizeof(struct in_conninfo14));
	printf("sizeof(in_conninfo 15)   = %zu\n", sizeof(struct in_conninfo15));
	printf("sizeof(xinpcb<=14)       = %zu\n", sizeof(struct xinpcb14));
	printf("sizeof(xinpcb 15)        = %zu\n", sizeof(struct xinpcb15));
	printf("sizeof(xinpgen)          = %zu\n", sizeof(struct xinpgen));
	printf("sizeof(xtcpcb)           = %zu\n", sizeof(struct xtcpcb));
	printf("sizeof(xfile)            = %zu\n", sizeof(struct xfile));
	printf("\n--- xinpcb<=14 (xinpcb-relative) ---\n");
	printf("xso_so   = %zu\n", offsetof(struct xinpcb14, xi_socket) + offsetof(struct xsocket, xso_so));
	printf("lport    = %zu\n", offsetof(struct xinpcb14, inp_inc) + offsetof(struct in_conninfo14, inc_ie) + offsetof(struct in_endpoints14, ie_lport));
	printf("v4 laddr = %zu\n", offsetof(struct xinpcb14, inp_inc) + offsetof(struct in_conninfo14, inc_ie) + offsetof(struct in_endpoints14, ie_dependladdr) + offsetof(struct in_addr_4in6, ia46_addr4));
	printf("v6 laddr = %zu\n", offsetof(struct xinpcb14, inp_inc) + offsetof(struct in_conninfo14, inc_ie) + offsetof(struct in_endpoints14, ie_dependladdr));
	printf("vflag    = %zu\n", offsetof(struct xinpcb14, inp_vflag));
	printf("\n--- xinpcb15 (xinpcb-relative) ---\n");
	printf("xso_so   = %zu\n", offsetof(struct xinpcb15, xi_socket) + offsetof(struct xsocket, xso_so));
	printf("lport    = %zu\n", offsetof(struct xinpcb15, inp_inc) + offsetof(struct in_conninfo15, inc_ie) + offsetof(struct in_endpoints15, ie_lport));
	printf("v4 laddr = %zu\n", offsetof(struct xinpcb15, inp_inc) + offsetof(struct in_conninfo15, inc_ie) + offsetof(struct in_endpoints15, ie_dependladdr) + offsetof(union in_dependaddr15, id4_addr));
	printf("v6 laddr = %zu\n", offsetof(struct xinpcb15, inp_inc) + offsetof(struct in_conninfo15, inc_ie) + offsetof(struct in_endpoints15, ie_dependladdr) + offsetof(union in_dependaddr15, id6_addr));
	printf("vflag    = %zu\n", offsetof(struct xinpcb15, inp_vflag));
	printf("\n--- xfile ---\n");
	printf("xf_pid   = %zu\n", offsetof(struct xfile, xf_pid));
	printf("xf_data  = %zu\n", offsetof(struct xfile, xf_data));
	return 0;
}
