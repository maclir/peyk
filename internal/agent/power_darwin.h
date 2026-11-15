#define PEYK_WILL_SLEEP 1
#define PEYK_POWERED_ON 2

int peyk_power_register(void);
void peyk_power_loop(void);
void peyk_power_allow(long id);
int peyk_power_capabilities(void);
