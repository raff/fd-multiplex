#include <multiplex.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

int main(void) {
    char buffer[256];
    int ch = 0;
    Multiplex * m = multiplex_new(1);

    srand(time(0));
    while (1) {
        ch = rand()%256;
        sprintf(buffer, "Hello on Channel %d.", ch);
        multiplex_send(m, ch, buffer, strlen(buffer));
        usleep(rand()%1000000);
    }

    return 0;
}
